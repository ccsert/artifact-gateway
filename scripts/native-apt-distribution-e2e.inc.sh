# Sourced by the isolated APT Hosted drill after source installation succeeds.
# Both requests use the repository-global pool identity and a different target
# suite, proving the target index is rebuilt instead of copying source Release.
apt_distribution_gate() {
  local operation=$1 target_name=$2 target_response target_id request_response request_body code state_file status
  target_response="$workdir/$operation-target.json"
  code=$("${curl_request[@]}" --output "$target_response" --write-out '%{http_code}' \
    --request POST "$gateway_url/api/v2/repositories" --header "Authorization: Bearer $admin_token" \
    --header 'Content-Type: application/json' --header "Idempotency-Key: apt-$operation-target" \
    --data "{\"name\":\"$target_name\",\"format\":\"apt\",\"type\":\"hosted\"}")
  [[ "$code" == 201 ]] || { cat "$target_response" >&2; return 1; }
  target_id=$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "$target_response")
  [[ -n "$target_id" ]] || return 1
  request_body=$(printf '{"targetRepositoryId":"%s","coordinate":"pool/main/a/artifact-gateway-e2e/artifact-gateway-e2e_1.0.0-1_all.deb","digest":"sha256:%s","aptTargetSuite":"candidate"}' "$target_id" "$package_sha256")
  request_response="$workdir/$operation-request.json"
  code=$("${curl_request[@]}" --output "$request_response" --write-out '%{http_code}' \
    --request POST "$gateway_url/api/v2/repositories/$repository_id/$operation" \
    --header "Authorization: Bearer $admin_token" --header 'Content-Type: application/json' \
    --header "Idempotency-Key: apt-$operation-distribution" --data "$request_body")
  [[ "$code" == 202 ]] || { cat "$request_response" >&2; return 1; }
  state_file="$workdir/$operation-signing-state.json"
  status=waiting
  for _ in $(seq 1 150); do
    "${curl_request[@]}" --fail --output "$state_file" --header "Authorization: Bearer $admin_token" \
      "$gateway_url/api/v2/repositories/$target_id/apt/signing-state"
    if grep -q '"suite":"candidate"' "$state_file" && grep -q '"state":"visible"' "$state_file"; then
      status=visible
      break
    fi
    sleep 1
  done
  [[ "$status" == visible ]] || { cat "$state_file" >&2; return 1; }
  "${curl_request[@]}" --fail --header "Authorization: Bearer $resolver_token" \
    "$gateway_url/apt/$target_name/dists/candidate/Release" > "$workdir/$operation-Release"
  grep -Fxq 'Suite: candidate' "$workdir/$operation-Release"
  apt_install 1.0.0-1 "$target_name" candidate
  printf 'APT %s: target-signed candidate suite installed successfully.\n' "$operation"
}

apt_distribution_gate promotions apt-promotion-e2e
apt_distribution_gate replications apt-replication-e2e
