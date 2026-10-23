package rubygemspublication_test

import (
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	d "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	f "github.com/ccsert/artifact-gateway/internal/testsupport/rubygemsfixture"
	"github.com/google/uuid"
)

func TestRubyGemsMemoryPublicationStoreContract(t *testing.T) {
	repos := repository.NewMemoryStore()
	repo, e := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "ruby-memory-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
	if e != nil {
		t.Fatal(e)
	}
	f.StoreContract(t, d.NewMemory(repos, repos), repo)
}

func TestRubyGemsMemoryGovernanceStoreContract(t *testing.T) {
	repos := repository.NewMemoryStore()
	repo, err := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "ruby-governance-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	f.GovernanceContract(t, d.NewMemory(repos, repos), repo)
}
