# Sourced by native-apt-hosted-e2e.sh after restoring the original signed backup.
# Every database mutation below targets that script's isolated Compose project.
lifecycle_url="$gateway_url/api/v2/repositories/$repository_id/apt"
lifecycle_request="$workdir/lifecycle-request.json"
python3 - "$snapshot_id" "$session_id" >"$lifecycle_request" <<'PY'
import json,sys
print(json.dumps({"suite":"stable","expectedSnapshotId":sys.argv[1],"action":"delete","publicationSessionIds":[sys.argv[2]]}))
PY
lifecycle_post() {
  local path=$1 key=$2 file=$3
  "${curl_request[@]}" --fail --request POST "$lifecycle_url/$path" \
    --header "Authorization: Bearer $admin_token" --header 'Content-Type: application/json' \
    --header "Idempotency-Key: $key" --data-binary "@$file"
}
lifecycle_state() {
  "${curl_request[@]}" --fail --header "Authorization: Bearer $admin_token" "$lifecycle_url/lifecycle?suite=stable"
}

# The signer is deliberately stopped. Failure must preserve the old Release.
failed_status=$("${curl_request[@]}" --output "$workdir/lifecycle-failed.json" --write-out '%{http_code}' \
  --request POST "$lifecycle_url/lifecycle" --header "Authorization: Bearer $admin_token" \
  --header 'Content-Type: application/json' --header 'Idempotency-Key: apt-e2e-delete' --data-binary "@$lifecycle_request")
[[ "$failed_status" == 503 ]] || { cat "$workdir/lifecycle-failed.json" >&2; exit 1; }
signing_state >"$workdir/lifecycle-failure-state.json"
cmp "$original_state" "$workdir/lifecycle-failure-state.json"
"${compose[@]}" up -d --force-recreate --wait reference-apt-signer
lifecycle_post lifecycle/preview apt-e2e-delete-preview "$lifecycle_request" >"$workdir/lifecycle-preview.json"
lifecycle_post lifecycle apt-e2e-delete "$lifecycle_request" >"$workdir/lifecycle-empty.json"
lifecycle_post lifecycle apt-e2e-delete "$lifecycle_request" >"$workdir/lifecycle-replay.json"
cmp "$workdir/lifecycle-empty.json" "$workdir/lifecycle-replay.json"

# Fresh clients accept a fully signed empty index; cached clients still fetch
# the immutable package and by-hash bytes from the retired snapshot.
"${curl_request[@]}" --fail --user "resolver:$resolver_token" \
  "$gateway_url/apt/apt-hosted-e2e/pool/main/a/artifact-gateway-e2e/artifact-gateway-e2e_1.0.0-1_all.deb" >"$workdir/lifecycle-old-package.deb"
cmp "$package_file" "$workdir/lifecycle-old-package.deb"
docker run --rm --network "$gateway_network" \
  --env "APT_E2E_RESOLVER_TOKEN=$resolver_token" --volume "$public_key:/keys/artifact-gateway.asc:ro" \
  "$debian_image" /bin/sh -ec '
    rm -f /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources
    install -d -m 0755 /etc/apt/keyrings /etc/apt/auth.conf.d
    install -m 0644 /keys/artifact-gateway.asc /etc/apt/keyrings/artifact-gateway.asc
    printf "machine http://gateway:8080/apt/apt-hosted-e2e\nlogin resolver\npassword %s\n" "$APT_E2E_RESOLVER_TOKEN" > /etc/apt/auth.conf.d/artifact-gateway.conf
    chmod 0600 /etc/apt/auth.conf.d/artifact-gateway.conf
    printf "%s\n" "deb [arch=all signed-by=/etc/apt/keyrings/artifact-gateway.asc] http://gateway:8080/apt/apt-hosted-e2e stable main" > /etc/apt/sources.list.d/artifact-gateway.list
    apt-get -o Acquire::Retries=0 update
    test -z "$(apt-cache policy artifact-gateway-e2e)"
  '

lifecycle_state >"$workdir/lifecycle-state.json"
python3 - "$workdir/lifecycle-state.json" >"$lifecycle_request" <<'PY'
import json,sys
state=json.load(open(sys.argv[1]))
current=next(x['snapshot'] for x in state['snapshots'] if x['snapshot']['state']=='visible')
assert len(state['packages'])==0 and len(state['deletions'])==1
print(json.dumps({'suite':'stable','expectedSnapshotId':current['id'],'action':'restore','deletionIds':[state['deletions'][0]['id']]}))
PY
lifecycle_post lifecycle apt-e2e-restore "$lifecycle_request" >"$workdir/lifecycle-restored.json"
apt_install

# Publish a second version, then retain the newest upload. Time aging is done
# only in this throwaway test database, never through a production API flag.
package_v2="$workdir/artifact-gateway-e2e_2.0.0-1_all.deb"
docker run --rm --volume "$workdir:/work" "$debian_image" /bin/sh -ec '
  dpkg-deb -R /work/artifact-gateway-e2e_1.0.0-1_all.deb /tmp/package
  sed -i "s/Version: 1.0.0-1/Version: 2.0.0-1/" /tmp/package/DEBIAN/control
  dpkg-deb --build --root-owner-group /tmp/package /work/artifact-gateway-e2e_2.0.0-1_all.deb >/dev/null
'
python3 - "$package_v2" >"$workdir/lifecycle-v2-session-request.json" <<'PY'
import hashlib,json,os,sys
path=sys.argv[1]
print(json.dumps({'suite':'stable','component':'main','objectName':os.path.basename(path),'declaredDigest':'sha256:'+hashlib.file_digest(open(path,'rb'),'sha256').hexdigest(),'declaredSize':os.path.getsize(path)}))
PY
lifecycle_post publication-sessions apt-e2e-v2 "$workdir/lifecycle-v2-session-request.json" >"$workdir/lifecycle-v2-session.json"
session_v2=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["id"])' "$workdir/lifecycle-v2-session.json")
"${curl_request[@]}" --fail --request PUT "$lifecycle_url/publication-sessions/$session_v2/package" \
  --header "Authorization: Bearer $admin_token" --header 'Content-Type: application/vnd.debian.binary-package' \
  --data-binary "@$package_v2" >"$workdir/lifecycle-v2-revision.json"
python3 - "$workdir/lifecycle-restored.json" "$session_id" "$session_v2" >"$workdir/lifecycle-two-versions.json" <<'PY'
import json,sys
snapshot=json.load(open(sys.argv[1]))
print(json.dumps({'suite':'stable','sequence':snapshot['sequence']+1,'publicationSessionIds':sys.argv[2:]}))
PY
lifecycle_post snapshots apt-e2e-two-versions "$workdir/lifecycle-two-versions.json" >"$workdir/lifecycle-two-versions-result.json"
"${compose[@]}" exec -T postgres psql -X -U gateway -d gateway -v ON_ERROR_STOP=1 -v "session=$session_id" <<'SQL'
UPDATE native_apt_package_revisions SET created_at=clock_timestamp()-interval '2 days'
WHERE id=(SELECT package_revision_id FROM native_apt_publication_sessions WHERE id=:'session'::uuid);
SQL
python3 - "$workdir/lifecycle-two-versions-result.json" >"$lifecycle_request" <<'PY'
import json,sys
print(json.dumps({'suite':'stable','expectedSnapshotId':json.load(open(sys.argv[1]))['id'],'action':'retention','keepLatest':1,'olderThanDays':1}))
PY
lifecycle_post lifecycle/preview apt-e2e-retention-preview "$lifecycle_request" >"$workdir/lifecycle-retention-preview.json"
python3 - "$workdir/lifecycle-retention-preview.json" "$session_id" <<'PY'
import json,sys
plan=json.load(open(sys.argv[1])); assert plan['removeSessionIds']==[sys.argv[2]] and plan['remainingPackages']==1
PY
python3 - "$workdir/lifecycle-retention-preview.json" "$lifecycle_request" <<'PYPLAN'
import json,sys
plan=json.load(open(sys.argv[1]));path=sys.argv[2];request=json.load(open(path))
request['publicationSessionIds']=plan['removeSessionIds']
with open(path,'w') as out: json.dump(request,out)
PYPLAN
lifecycle_post lifecycle apt-e2e-retention "$lifecycle_request" >"$workdir/lifecycle-retained.json"
apt_install 2.0.0-1
lifecycle_state >"$workdir/lifecycle-state.json"
python3 - "$workdir/lifecycle-state.json" >"$lifecycle_request" <<'PY'
import json,sys
state=json.load(open(sys.argv[1]));current=next(x['snapshot'] for x in state['snapshots'] if x['snapshot']['state']=='visible')
active=[d['id'] for d in state['deletions'] if d['state']=='recoverable'];assert len(active)==1
print(json.dumps({'suite':'stable','expectedSnapshotId':current['id'],'action':'restore','deletionIds':active}))
PY
lifecycle_post lifecycle apt-e2e-retention-restore "$lifecycle_request" >"$workdir/lifecycle-retention-restored.json"
apt_install 1.0.0-1
printf 'APT lifecycle client gate passed: signer failure/retry, empty signed index, retained pool read, retention, restore, and both version installations.\n'
