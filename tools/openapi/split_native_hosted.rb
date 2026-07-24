#!/usr/bin/env ruby
# frozen_string_literal: true

# One-time migration helper. It turns the old JSON source into the reviewed,
# multi-file YAML source tree; it is intentionally kept so a future format
# migration can reproduce the initial split.
require "fileutils"
require "json"
require "yaml"

root = File.expand_path("../..", __dir__)
source = JSON.parse(File.read(File.join(root, "api/openapi/native-hosted-v1.json")))
output = File.join(root, "api/openapi")

FileUtils.mkdir_p(["components", "management", "protocols"].map { |directory| File.join(output, directory) })

def rewrite_references(value, from)
  case value
  when Hash
    value.transform_values { |entry| rewrite_references(entry, from) }.tap do |result|
      next unless result["$ref"]

      result["$ref"] = result["$ref"]
        .gsub("#/components/schemas/", from == :schemas ? "#/" : "../components/schemas.yaml#/")
        .gsub("#/components/responses/", "../components/responses.yaml#/")
        .gsub("#/components/parameters/", "../components/parameters.yaml#/")
    end
  when Array
    value.map { |entry| rewrite_references(entry, from) }
  else
    value
  end
end

def write_yaml(path, value)
  File.write(path, YAML.dump(value))
end

def fragment_key(path)
  path
    .delete_prefix("/")
    .gsub(/[{}]/, "")
    .gsub(/[^a-zA-Z0-9]+/, "_")
    .sub(/_+\z/, "")
end

components = source.fetch("components")
write_yaml(File.join(output, "components/schemas.yaml"), rewrite_references(components.fetch("schemas"), :schemas))
write_yaml(File.join(output, "components/parameters.yaml"), components.fetch("parameters"))
write_yaml(File.join(output, "components/responses.yaml"), rewrite_references(components.fetch("responses"), :responses))
write_yaml(File.join(output, "components/security-schemes.yaml"), components.fetch("securitySchemes"))

path_groups = {
  "management/repositories.yaml" => ["/repositories", "/repositories/{repositoryId}", "/repositories/{repositoryId}/grants", "/repositories/{repositoryId}/retention-policy"],
  "management/groups.yaml" => ["/groups", "/groups/{groupId}", "/groups/{groupId}/members"],
  "management/sessions.yaml" => ["/repositories/{repositoryId}/publish-sessions", "/publish-sessions/{sessionId}", "/publish-sessions/{sessionId}/objects/{objectName}", "/publish-sessions/{sessionId}:commit"],
  "management/artifacts.yaml" => ["/repositories/{repositoryId}/artifacts", "/repositories/{repositoryId}/artifacts/{artifactId}"],
  "protocols/raw.yaml" => ["/raw/{repository}/{path}"],
  "protocols/oci.yaml" => ["/v2/{name}/blobs/uploads/", "/v2/{name}/blobs/uploads/{uuid}", "/v2/{name}/blobs/{digest}", "/v2/{name}/manifests/{reference}", "/v2/{name}/tags/list"],
  "protocols/maven.yaml" => ["/repository/maven/{repository}/{assetPath}", "/repository/maven/{repository}/coordinates/{coordinate}:commit"]
}

protocol_overlays = {
  "protocols/raw.yaml" => {
    "officialSpecification" => "No protocol-wide Raw repository HTTP standard; this is a Gateway overlay.",
    "gatewayDecision" => "Use PUT, GET, HEAD, and DELETE at /raw/{repository}/{path}; path is a catch-all."
  },
  "protocols/oci.yaml" => {
    "officialSpecification" => "https://distribution.github.io/distribution/spec/api/",
    "gatewayDecision" => "Implement the supported Registry V2 upload, blob, manifest, and tag routes; exclude catalog, referrers, and direct blob deletion."
  },
  "protocols/maven.yaml" => {
    "officialSpecification" => "https://maven.apache.org/repositories/index.html",
    "gatewayDecision" => "Use standard PUT staging plus explicit Gateway coordinate commit; client metadata is not authoritative."
  }
}

paths = {}
path_groups.each do |file, names|
  fragment = {}
	fragment["x-gateway-protocol-overlay"] = protocol_overlays[file] if protocol_overlays.key?(file)
  names.each do |name|
    key = fragment_key(name)
    fragment[key] = rewrite_references(source.fetch("paths").fetch(name), :path)
    paths[name] = { "$ref" => "./#{file}#/#{key}" }
  end
  write_yaml(File.join(output, file), fragment)
end

write_yaml(File.join(output, "protocols/conan.yaml"), {
  "description" => "Conan 2 is a Gateway Group/Proxy read-through overlay. Native Hosted write, delete, search, and lifecycle routes are intentionally absent from this OpenAPI contract.",
  "officialSpecification" => "https://docs.conan.io/2/reference/commands/remote.html",
  "gatewayOverlay" => "docs/protocol-compatibility.md#conan"
})

component_refs = lambda do |file, names|
  names.to_h { |name| [name, { "$ref" => "./components/#{file}.yaml#/#{name}" }] }
end
root_document = source.slice("info", "servers", "security")
root_document["openapi"] = "3.1.0"
root_document["paths"] = paths
root_document["components"] = {
  "schemas" => component_refs.call("schemas", components.fetch("schemas").keys),
  "parameters" => component_refs.call("parameters", components.fetch("parameters").keys),
  "responses" => component_refs.call("responses", components.fetch("responses").keys),
  "securitySchemes" => component_refs.call("security-schemes", components.fetch("securitySchemes").keys)
}
root_document["x-gateway-protocol-overlays"] = {
  "oci" => { "officialSpecification" => "https://distribution.github.io/distribution/spec/api/" },
  "raw" => { "gatewayOverlay" => "docs/protocol-compatibility.md#raw-hosted" },
  "maven" => { "officialSpecification" => "https://maven.apache.org/repositories/index.html" },
  "conan" => { "$ref" => "./protocols/conan.yaml" }
}
write_yaml(File.join(output, "native-hosted.yaml"), root_document)

management_document = root_document.dup
management_document["paths"] = paths.select { |name, _| name.start_with?("/repositories", "/groups", "/publish-sessions") }
write_yaml(File.join(output, "management.yaml"), management_document)

runtime_document = root_document.dup
runtime_document["paths"] = paths.slice("/repositories", "/repositories/{repositoryId}")
write_yaml(File.join(output, "management-runtime.yaml"), runtime_document)
