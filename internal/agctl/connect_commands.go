package agctl

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
)

// tokenPlaceholder stands for the credential in every generated snippet. The
// configured token is never written into a snippet.
const tokenPlaceholder = "${ARTIFACT_GATEWAY_TOKEN}"

// clientUser is the non-empty label native clients need as a username; the
// Gateway authorizes the credential, not the label.
const clientUser = "ci"

func repoCapabilitiesCommand() command {
	return command{
		name:        "repo capabilities",
		synopsis:    "show the operations a hosted repository's format supports",
		usage:       "agctl repo capabilities ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource:     "repository",
			Scenarios:    []string{"ci", "admin"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath + "/{repositoryId}/capabilities"},
			MinimumRole:  "repositories:read",
			OutputSchema: "RepositoryCapabilities",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: documentCommand[adminopenapi.RepositoryCapabilities]{
			request: func(env *commandContext) (string, url.Values, error) {
				return repositorySubPath(env.args[0], "capabilities"), nil, nil
			},
			render: func(w io.Writer, capabilities adminopenapi.RepositoryCapabilities) error {
				operations := make([]string, 0, len(capabilities.Operations))
				for _, operation := range capabilities.Operations {
					operations = append(operations, string(operation))
				}
				return keyValues(w, [][2]string{
					{"format", string(capabilities.Format)},
					{"type", string(capabilities.Type)},
					{"operations", strings.Join(operations, ", ")},
					{"artifact scanning", yesNo(capabilities.ArtifactScanning)},
					{"publication scanning", yesNo(capabilities.PublicationScanning)},
				})
			},
		}.run,
	}
}

// connectSnippet is one piece of client configuration: a file to write or a
// command to run.
type connectSnippet struct {
	Label   string `json:"label"`
	File    string `json:"file,omitempty"`
	Content string `json:"content"`
}

type connectGuide struct {
	Repository struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Format string `json:"format"`
		Type   string `json:"type"`
	} `json:"repository"`
	BaseURL    string           `json:"baseUrl"`
	Credential string           `json:"credential"`
	Snippets   []connectSnippet `json:"snippets"`
	Notes      []string         `json:"notes"`
}

// connectTarget names the Gateway the snippets point at: the configured
// server without credentials, query, or fragment.
type connectTarget struct {
	base     string // https://gateway.example.com[/prefix]
	host     string // gateway.example.com[:port]
	hostname string // bare hostname for pip's .netrc lookup
	path     string // gateway.example.com[:port][/prefix], for npm lines
}

func newConnectTarget(server *url.URL) connectTarget {
	clean := url.URL{Scheme: server.Scheme, Host: server.Host, Path: strings.TrimSuffix(server.Path, "/")}
	return connectTarget{base: clean.String(), host: clean.Host, hostname: clean.Hostname(), path: clean.Host + clean.Path}
}

// connectSnippets builds the client configuration for one repository. The
// URL layouts are the Gateway's protocol mounts, the same the Console's usage
// guides and docs/service-account-operations.md show.
func connectSnippets(target connectTarget, name string, format adminopenapi.Format, hosted bool) ([]connectSnippet, []string) {
	base := target.base
	var snippets []connectSnippet
	var notes []string
	switch format {
	case adminopenapi.FormatNpm:
		registry := base + "/npm/" + name + "/"
		snippets = append(snippets, connectSnippet{Label: "npm configuration", File: ".npmrc", Content: fmt.Sprintf(
			"registry=%s\n//%s/npm/%s/:_authToken=%s\nalways-auth=true", registry, target.path, name, tokenPlaceholder)})
		if hosted {
			snippets = append(snippets, connectSnippet{Label: "publish", Content: "npm publish --registry " + registry})
		}
	case adminopenapi.FormatPypi:
		index := base + "/pypi/" + name + "/simple/"
		snippets = append(snippets,
			connectSnippet{Label: "pip configuration", File: "pip.conf", Content: "[global]\nindex-url = " + index},
			connectSnippet{Label: "pip credentials", File: ".netrc", Content: fmt.Sprintf("machine %s\nlogin %s\npassword %s", target.hostname, clientUser, tokenPlaceholder)})
		if hosted {
			snippets = append(snippets,
				connectSnippet{Label: "twine configuration", File: ".pypirc", Content: fmt.Sprintf(
					"[distutils]\nindex-servers = %s\n\n[%s]\nrepository = %s/pypi/%s/legacy/\nusername = %s\npassword = %s", name, name, base, name, clientUser, tokenPlaceholder)},
				connectSnippet{Label: "publish", Content: "twine upload --repository " + name + " dist/*"})
		}
	case adminopenapi.FormatMaven:
		repository := base + "/maven/" + name + "/"
		snippets = append(snippets,
			connectSnippet{Label: "Maven credentials", File: "settings.xml", Content: fmt.Sprintf(
				"<servers>\n  <server>\n    <id>%s</id>\n    <username>%s</username>\n    <password>${env.ARTIFACT_GATEWAY_TOKEN}</password>\n  </server>\n</servers>", name, clientUser)},
			connectSnippet{Label: "Maven repository", File: "pom.xml", Content: fmt.Sprintf(
				"<repositories>\n  <repository>\n    <id>%s</id>\n    <url>%s</url>\n  </repository>\n</repositories>", name, repository)})
		if hosted {
			snippets = append(snippets, connectSnippet{Label: "Maven deployment", File: "pom.xml", Content: fmt.Sprintf(
				"<distributionManagement>\n  <repository>\n    <id>%s</id>\n    <url>%s</url>\n  </repository>\n</distributionManagement>", name, repository)})
		}
	case adminopenapi.FormatGo:
		proxy := base + "/go/" + name
		snippets = append(snippets,
			connectSnippet{Label: "Go proxy", Content: "go env -w GOPROXY=" + proxy},
			connectSnippet{Label: "Go credentials", File: ".netrc", Content: fmt.Sprintf("machine %s\nlogin %s\npassword %s", target.host, clientUser, tokenPlaceholder)})
	case adminopenapi.FormatOci:
		image := target.path + "/" + name + "/<image>:<tag>"
		snippets = append(snippets,
			connectSnippet{Label: "sign in", Content: fmt.Sprintf("printf '%%s' \"$ARTIFACT_GATEWAY_TOKEN\" | docker login %s --username %s --password-stdin", target.host, clientUser)},
			connectSnippet{Label: "pull", Content: "docker pull " + image})
		if hosted {
			snippets = append(snippets, connectSnippet{Label: "push", Content: "docker tag <local-image>:<tag> " + image + "\ndocker push " + image})
		}
	case adminopenapi.FormatRaw:
		root := base + "/raw/" + name
		snippets = append(snippets, connectSnippet{Label: "download", Content: fmt.Sprintf(
			"curl --fail --location -H \"Authorization: Bearer $ARTIFACT_GATEWAY_TOKEN\" --output <file> %s/<path>", root)})
		if hosted {
			snippets = append(snippets, connectSnippet{Label: "upload", Content: fmt.Sprintf(
				"curl --fail -H \"Authorization: Bearer $ARTIFACT_GATEWAY_TOKEN\" --upload-file <file> %s/<path>", root)})
		}
	case adminopenapi.FormatConan:
		snippets = append(snippets,
			connectSnippet{Label: "add remote", Content: fmt.Sprintf("conan remote add %s %s/conan/v2/%s", name, base, name)},
			connectSnippet{Label: "sign in", Content: fmt.Sprintf("conan remote login %s %s -p \"$ARTIFACT_GATEWAY_TOKEN\"", name, clientUser)})
		if hosted {
			snippets = append(snippets, connectSnippet{Label: "upload", Content: fmt.Sprintf("conan upload \"<reference>\" -r %s", name)})
		}
	case adminopenapi.FormatApt:
		snippets = append(snippets,
			connectSnippet{Label: "APT source", File: "/etc/apt/sources.list.d/" + name + ".list", Content: fmt.Sprintf("deb %s/apt/%s <suite> <component>", base, name)},
			connectSnippet{Label: "APT credentials", File: "/etc/apt/auth.conf.d/" + name + ".conf", Content: fmt.Sprintf("machine %s/apt/%s\nlogin %s\npassword %s", target.path, name, clientUser, tokenPlaceholder)})
		if hosted {
			notes = append(notes, "APT hosted repositories are a management-only preview: clients cannot install from them until signed snapshots are published")
		}
	case adminopenapi.FormatCargo:
		snippets = append(snippets,
			connectSnippet{Label: "Cargo registry", File: ".cargo/config.toml", Content: fmt.Sprintf(
				"[registries.%s]\nindex = \"sparse+%s/cargo/%s/\"\ncredential-provider = \"cargo:token\"", name, base, name)},
			connectSnippet{Label: "sign in", Content: fmt.Sprintf("printf '%%s' \"$ARTIFACT_GATEWAY_TOKEN\" | cargo login --registry %s", name)})
		if hosted {
			snippets = append(snippets, connectSnippet{Label: "publish", Content: "cargo publish --registry " + name})
		}
	}
	return snippets, notes
}

func repoConnectCommand() command {
	return command{
		name:        "repo connect",
		synopsis:    "print client configuration for a repository (credentials as placeholders)",
		usage:       "agctl repo connect ID [--json]",
		positionals: 1,
		argsUsage:   "ID",
		meta: commandMeta{
			Resource:     "repository",
			Scenarios:    []string{"ci"},
			Stage:        "m1",
			Endpoints:    []string{"GET " + repositoriesPath + "/{repositoryId}"},
			MinimumRole:  "repositories:read",
			OutputSchema: "ConnectGuide",
			DryRun:       "unsupported",
			Retry:        true,
		},
		run: func(ctx context.Context, env *commandContext) error {
			client, err := env.client()
			if err != nil {
				return err
			}
			var repo adminopenapi.Repository
			if err := client.getJSON(ctx, repositoryPath(env.args[0]), nil, &repo); err != nil {
				return err
			}
			kind := repositoryType(repo)
			target := newConnectTarget(client.baseURL)
			guide := connectGuide{BaseURL: target.base, Credential: tokenPlaceholder}
			guide.Repository.ID, guide.Repository.Name, guide.Repository.Format, guide.Repository.Type = repo.Id, repo.Name, string(repo.Format), kind
			guide.Snippets, guide.Notes = connectSnippets(target, repo.Name, repo.Format, kind == string(adminopenapi.RepositoryTypeHosted))
			if len(guide.Snippets) == 0 {
				return &UsageError{Message: fmt.Sprintf("agctl repo connect does not know the %s format yet", repo.Format), Hint: "see the repository's usage guide in the Console"}
			}
			if repo.State != adminopenapi.RepositoryStateActive {
				guide.Notes = append(guide.Notes, fmt.Sprintf("the repository is %s: clients cannot use it", repo.State))
			}
			if guide.Notes == nil {
				guide.Notes = []string{}
			}
			return env.render(guide, nil, func(w io.Writer) error { return renderConnectGuide(w, guide) })
		},
	}
}

func renderConnectGuide(w io.Writer, guide connectGuide) error {
	if _, err := fmt.Fprintf(w, "# %s (%s %s) at %s\n# set ARTIFACT_GATEWAY_TOKEN to a credential with access to this repository\n",
		guide.Repository.Name, guide.Repository.Format, guide.Repository.Type, guide.BaseURL); err != nil {
		return err
	}
	for _, note := range guide.Notes {
		if _, err := fmt.Fprintf(w, "# note: %s\n", note); err != nil {
			return err
		}
	}
	for _, snippet := range guide.Snippets {
		header := snippet.Label
		if snippet.File != "" {
			header += " — " + snippet.File
		}
		if _, err := fmt.Fprintf(w, "\n## %s\n%s\n", header, snippet.Content); err != nil {
			return err
		}
	}
	return nil
}
