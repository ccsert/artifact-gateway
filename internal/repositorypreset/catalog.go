// Package repositorypreset defines the repositories created on a new installation.
// The catalog is versioned independently from later administrator changes.
package repositorypreset

import "github.com/ccsert/artifact-gateway/internal/repository"

const Version = "standard-v1"

type Group struct {
	Name    string
	Format  repository.Format
	Members []string
}

type Catalog struct {
	Repositories []repository.HostedRepository
	Groups       []Group
}

// Standard returns a fresh catalog. Only admitted formats are included;
// credential-dependent registries and preview protocols require explicit setup.
func Standard() Catalog {
	formats := []repository.Format{
		repository.FormatRaw, repository.FormatOCI, repository.FormatMaven,
		repository.FormatNPM, repository.FormatPyPI, repository.FormatGo,
		repository.FormatCargo, repository.FormatConan,
	}
	catalog := Catalog{}
	for _, format := range formats {
		catalog.Repositories = append(catalog.Repositories, repository.HostedRepository{
			Name: string(format) + "-hosted", Format: format,
			Type: repository.RepositoryTypeHosted,
		})
	}
	proxies := []repository.HostedRepository{
		{Name: "maven-central", Format: repository.FormatMaven, Endpoint: "https://repo.maven.apache.org/maven2", AllowedHosts: []string{"repo.maven.apache.org"}},
		{Name: "npmjs", Format: repository.FormatNPM, Endpoint: "https://registry.npmjs.org", AllowedHosts: []string{"registry.npmjs.org"}},
		{Name: "pypi-proxy", Format: repository.FormatPyPI, Endpoint: "https://pypi.org", AllowedHosts: []string{"pypi.org", "files.pythonhosted.org"}},
		{Name: "go-proxy", Format: repository.FormatGo, Endpoint: "https://proxy.golang.org", AllowedHosts: []string{"proxy.golang.org", "storage.googleapis.com"}},
		{Name: "crates-io", Format: repository.FormatCargo, Endpoint: "https://index.crates.io", AllowedHosts: []string{"index.crates.io", "static.crates.io"}},
	}
	for _, proxy := range proxies {
		proxy.Type = repository.RepositoryTypeProxy
		proxy.EgressProxy = &repository.EgressProxy{Mode: repository.EgressProxyModeEnvironment}
		catalog.Repositories = append(catalog.Repositories, proxy)
		catalog.Groups = append(catalog.Groups, Group{
			Name: string(proxy.Format) + "-all", Format: proxy.Format,
			Members: []string{string(proxy.Format) + "-hosted", proxy.Name},
		})
	}
	return catalog
}

type Result struct {
	State        string
	Version      string
	Repositories int
	Groups       int
}
