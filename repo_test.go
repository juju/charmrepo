// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmrepo_test // import "gopkg.in/juju/charmrepo.v3"

import (
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6"

	"gopkg.in/juju/charmrepo.v3"
	"gopkg.in/juju/charmrepo.v3/csclient"
	charmtesting "gopkg.in/juju/charmrepo.v3/testing"
)

var TestCharms = charmtesting.NewRepo("internal/test-charm-repo", "quantal")

type inferRepoSuite struct{}

var _ = gc.Suite(&inferRepoSuite{})

var inferRepositoryTests = []struct {
	url           string
	localRepoPath string
	err           string
}{{
	url: "cs:trusty/django",
}, {
	url: "local:precise/wordpress",
	err: "path to local repository not specified",
}, {
	url:           "local:precise/haproxy-47",
	localRepoPath: "/tmp/repo-path",
}}

func (s *inferRepoSuite) TestInferRepository(c *gc.C) {
	for i, test := range inferRepositoryTests {
		c.Logf("test %d: %s", i, test.url)
		ref := charm.MustParseURL(test.url)
		repo, err := charmrepo.InferRepository(
			ref, charmrepo.NewCharmStoreParams{}, test.localRepoPath)
		if test.err != "" {
			c.Assert(err, gc.ErrorMatches, test.err)
			c.Assert(repo, gc.IsNil)
			continue
		}
		c.Assert(err, jc.ErrorIsNil)
		switch store := repo.(type) {
		case *charmrepo.LocalRepository:
			c.Assert(store.Path, gc.Equals, test.localRepoPath)
		case *charmrepo.CharmStore:
			c.Assert(store.URL(), gc.Equals, csclient.ServerURL)
		default:
			c.Fatal("unknown repository type")
		}
	}
}
