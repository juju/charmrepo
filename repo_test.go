// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo_test

import (
	"fmt"
	"path/filepath"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"gopkg.in/juju/charmrepo.v2-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient"
	charmtesting "gopkg.in/juju/charmrepo.v2-unstable/testing"
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
}, {
	url: filepath.Join(TestCharms.Path(), "quantal", "riak"),
}, {
	url: ".",
	err: "not a valid charm path: .",
}, {
	url: filepath.Join(TestCharms.Path(), "foo"),
	err: fmt.Sprintf("not a valid charm path: %v", filepath.Join(TestCharms.Path(), "foo")),
}}

func (s *inferRepoSuite) TestInferRepository(c *gc.C) {
	for i, test := range inferRepositoryTests {
		c.Logf("test %d: %s", i, test.url)
		repo, err := charmrepo.InferRepository(
			test.url, charmrepo.NewCharmStoreParams{}, test.localRepoPath)
		if test.err != "" {
			c.Assert(err, gc.ErrorMatches, test.err)
			c.Assert(repo, gc.IsNil)
			continue
		}
		c.Assert(err, jc.ErrorIsNil)
		if local, ok := charmrepo.MaybeLocalRepository(repo); ok {
			c.Assert(local.Path, gc.Equals, test.localRepoPath)
		} else if local, ok := charmrepo.MaybeCharmPath(repo); ok {
			c.Assert(local.Path, gc.Equals, test.url)
		} else {
			switch store := repo.(type) {
			case *charmrepo.CharmStore:
				c.Assert(store.URL(), gc.Equals, csclient.ServerURL)
			default:
				c.Fatal("unknown repository type")
			}
		}
	}
}
