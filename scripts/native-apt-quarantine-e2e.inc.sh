# Sourced by native-apt-hosted-e2e.sh with its isolated repository and signer.
# Cache a real APT index, then prove that it cannot bypass a later quarantine.
quarantine_pool='pool/main/a/artifact-gateway-e2e/artifact-gateway-e2e_1.0.0-1_all.deb'
quarantine_query=$(python3 - "$quarantine_pool" "$package_file" <<'PY'
import hashlib,sys,urllib.parse
print(urllib.parse.urlencode({'coordinate':sys.argv[1],'digest':'sha256:'+hashlib.file_digest(open(sys.argv[2],'rb'),'sha256').hexdigest()}))
PY
)
quarantine_url="$gateway_url/api/v2/repositories/$repository_id/artifact-quarantine?$quarantine_query"
quarantine_policy_url="$gateway_url/api/v2/repositories/$repository_id/quarantine-read-policy"
mkdir -p "$workdir/quarantine-apt-lists"
apt_cached_quarantine_client() {
  docker run --rm --network "$gateway_network" \
    --env "APT_E2E_RESOLVER_TOKEN=$resolver_token" \
    --volume "$public_key:/keys/artifact-gateway.asc:ro" \
    --volume "$workdir/quarantine-apt-lists:/var/lib/apt/lists" \
    "$debian_image" /bin/sh -ec '
      rm -f /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources
      install -d -m 0755 /etc/apt/keyrings /etc/apt/auth.conf.d
      install -m 0644 /keys/artifact-gateway.asc /etc/apt/keyrings/artifact-gateway.asc
      printf "machine http://gateway:8080/apt/apt-hosted-e2e\nlogin resolver\npassword %s\n" "$APT_E2E_RESOLVER_TOKEN" > /etc/apt/auth.conf.d/artifact-gateway.conf
      chmod 0600 /etc/apt/auth.conf.d/artifact-gateway.conf
      printf "%s\n" "deb [arch=all signed-by=/etc/apt/keyrings/artifact-gateway.asc] http://gateway:8080/apt/apt-hosted-e2e stable main" > /etc/apt/sources.list.d/artifact-gateway.list
      apt-get -o Acquire::Retries=0 "$@"
    ' -- "$@"
}
apt_cached_quarantine_client update
"${curl_request[@]}" --fail --user "resolver:$resolver_token" \
  "$gateway_url/apt/apt-hosted-e2e/dists/stable/InRelease" >"$workdir/quarantine-before-inrelease"
"${curl_request[@]}" --fail --user "resolver:$resolver_token" \
  "$gateway_url/apt/apt-hosted-e2e/dists/stable/main/binary-all/Packages" >"$workdir/quarantine-packages"
quarantine_index_hash=$(python3 - "$workdir/quarantine-packages" <<'PY'
import hashlib,sys
print(hashlib.file_digest(open(sys.argv[1],'rb'),'sha256').hexdigest())
PY
)
"${curl_request[@]}" --fail --request PUT "$quarantine_url" --header "Authorization: Bearer $admin_token" \
  --header 'If-Match: 0' --header 'Content-Type: application/json' \
  --data '{"state":"quarantined","reason":"cached-index acceptance"}' >"$workdir/quarantine-result.json"
# Default read policy remains compatible, including the signed bytes.
"${curl_request[@]}" --fail --user "resolver:$resolver_token" \
  "$gateway_url/apt/apt-hosted-e2e/dists/stable/InRelease" >"$workdir/quarantine-default-inrelease"
cmp "$workdir/quarantine-before-inrelease" "$workdir/quarantine-default-inrelease"
"${curl_request[@]}" --fail --request PUT "$quarantine_policy_url" --header "Authorization: Bearer $admin_token" \
  --header 'If-Match: 1' --header 'Content-Type: application/json' \
  --data '{"version":"1","enabled":true}' >"$workdir/quarantine-policy.json"
for path in "$quarantine_pool" dists/stable/InRelease dists/stable/Release dists/stable/Release.gpg \
  dists/stable/main/binary-all/Packages dists/stable/main/binary-all/Packages.gz \
  "dists/stable/main/binary-all/by-hash/SHA256/$quarantine_index_hash"; do
  for method in GET HEAD; do
    # HEAD must use curl's --head, otherwise curl waits for a non-existent body.
    request_method=(--request GET)
    if [[ "$method" == HEAD ]]; then request_method=(--head); fi
    status=$("${curl_request[@]}" "${request_method[@]}" --output /dev/null --write-out '%{http_code}' \
      --user "resolver:$resolver_token" --header 'If-None-Match: *' --header 'Range: bytes=0-1' \
      "$gateway_url/apt/apt-hosted-e2e/$path")
    [[ "$status" == 403 ]] || { printf 'quarantine bypass: %s %s HTTP %s\n' "$method" "$path" "$status" >&2; exit 1; }
  done
done
if apt_cached_quarantine_client install -y --no-install-recommends artifact-gateway-e2e=1.0.0-1 >"$workdir/quarantine-denied-install.log" 2>&1; then
  printf 'cached APT index bypassed quarantine\n' >&2; exit 1
fi
grep -q '403.*Forbidden' "$workdir/quarantine-denied-install.log" || { cat "$workdir/quarantine-denied-install.log" >&2; exit 1; }
"${curl_request[@]}" --fail --request PUT "$quarantine_url" --header "Authorization: Bearer $admin_token" \
  --header 'If-Match: 1' --header 'Content-Type: application/json' \
  --data '{"state":"released","reason":"cached-index acceptance completed"}' >"$workdir/quarantine-released.json"
"${curl_request[@]}" --fail --user "resolver:$resolver_token" \
  "$gateway_url/apt/apt-hosted-e2e/dists/stable/InRelease" >"$workdir/quarantine-after-inrelease"
cmp "$workdir/quarantine-before-inrelease" "$workdir/quarantine-after-inrelease"
apt_cached_quarantine_client install -y --no-install-recommends artifact-gateway-e2e=1.0.0-1
"${curl_request[@]}" --fail --request PUT "$quarantine_policy_url" --header "Authorization: Bearer $admin_token" \
  --header 'If-Match: 2' --header 'Content-Type: application/json' \
  --data '{"version":"2","enabled":false}' >"$workdir/quarantine-policy-disabled.json"
printf 'APT quarantine cached-index installation gate passed\n'
