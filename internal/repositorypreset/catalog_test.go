package repositorypreset_test

import (
	"net/url"
	"slices"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/repositorypreset"
)

func TestStandardCatalogOnlyCreatesAdmittedPrivateRepositories(t *testing.T) {
	catalog := repositorypreset.Standard()
	hosted, proxies := 0, 0
	names := map[string]repository.HostedRepository{}
	for _, repo := range catalog.Repositories {
		if _, exists := names[repo.Name]; exists {
			t.Fatalf("duplicate name %s", repo.Name)
		}
		names[repo.Name] = repo
		if !repository.FormatSupportsRepositoryType(repo.Format, repo.Type) {
			t.Fatalf("unadmitted repository: %+v", repo)
		}
		if repo.AnonymousRead || repo.UpstreamAuth != nil || repo.OCIBearer != nil {
			t.Fatalf("preset changes authorization: %+v", repo)
		}
		if repo.Type == repository.RepositoryTypeHosted {
			hosted++
			if repo.Endpoint != "" || len(repo.AllowedHosts) != 0 {
				t.Fatalf("hosted has upstream: %+v", repo)
			}
		} else {
			proxies++
			u, err := url.Parse(repo.Endpoint)
			if err != nil || u.Scheme != "https" || u.User != nil || !slices.Contains(repo.AllowedHosts, u.Hostname()) {
				t.Fatalf("invalid public upstream: %+v", repo)
			}
			if repo.EgressProxy == nil || repo.EgressProxy.Mode != repository.EgressProxyModeEnvironment || repo.EgressProxy.Validate() != nil {
				t.Fatalf("proxy ignores operator egress policy: %+v", repo)
			}
		}
	}
	if hosted != 8 || proxies != 5 || len(catalog.Groups) != 5 {
		t.Fatalf("hosted=%d proxies=%d groups=%d", hosted, proxies, len(catalog.Groups))
	}
	for _, group := range catalog.Groups {
		profile, ok := repository.FormatProfileFor(group.Format)
		if !ok || !profile.GroupSupported || len(group.Members) != 2 {
			t.Fatalf("invalid group: %+v", group)
		}
		for index, name := range group.Members {
			member, ok := names[name]
			if !ok || member.Format != group.Format {
				t.Fatalf("invalid member %s", name)
			}
			if (index == 0) != (member.Type == repository.RepositoryTypeHosted) {
				t.Fatalf("local publication does not precede upstream: %+v", group)
			}
		}
	}
}

func TestStandardCatalogReturnsIndependentConfiguration(t *testing.T) {
	first := repositorypreset.Standard()
	first.Repositories[8].AllowedHosts[0] = "modified.invalid"
	first.Repositories[8].EgressProxy.Mode = repository.EgressProxyModeDirect
	first.Groups[0].Members[0] = "modified"
	second := repositorypreset.Standard()
	if second.Repositories[8].AllowedHosts[0] != "repo.maven.apache.org" || second.Repositories[8].EgressProxy.Mode != repository.EgressProxyModeEnvironment || second.Groups[0].Members[0] != "maven-hosted" {
		t.Fatal("administrator changes mutated subsequent preset definitions")
	}
}
