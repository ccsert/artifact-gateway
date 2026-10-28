package rubygemspublication_test

import (
	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	f "github.com/ccsert/artifact-gateway/internal/testsupport/rubygemsfixture"
	"github.com/google/uuid"
	"testing"
)

func TestRubyGemsMemoryReplicationStoreContract(t *testing.T) {
	repos := repository.NewMemoryStore()
	store := domain.NewMemory(repos, repos)
	var source, target repository.HostedRepository
	for _, r := range []*repository.HostedRepository{&source, &target} {
		v, err := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "replication-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
		if err != nil {
			t.Fatal(err)
		}
		*r = v
	}
	f.ReplicationStoreContract(t, store, source, target)
}
