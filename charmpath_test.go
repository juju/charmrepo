// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo_test

import (
	"os"

	gitjujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"

	"github.com/juju/utils/fs"
	"gopkg.in/juju/charmrepo.v2-unstable"
	"path/filepath"
)

type charmPathRepoSuite struct {
	gitjujutesting.FakeHomeSuite
	repoPath string
}

var _ = gc.Suite(&charmPathRepoSuite{})

func (s *charmPathRepoSuite) SetUpTest(c *gc.C) {
	s.FakeHomeSuite.SetUpTest(c)
	s.repoPath = c.MkDir()
}

func (s *charmPathRepoSuite) cloneCharmDir(path, name string) string {
	return TestCharms.ClonedDirPath(path, name)
}

func (s *charmPathRepoSuite) checkNotFoundErr(c *gc.C, err error, path string, charmURL *charm.URL) {
	expect := `entity not found in "` + path + `": ` + charmURL.String()
	c.Check(err, gc.ErrorMatches, expect)
}

func (s *charmPathRepoSuite) TestMismatchedCharmName(c *gc.C) {
	charmDir := filepath.Join(s.repoPath, "mysql")
	s.cloneCharmDir(s.repoPath, "mysql")
	repo, err := charmrepo.NewCharmPath(charmDir)
	c.Assert(err, jc.ErrorIsNil)
	charmURL := charm.MustParseURL("local:quantal/foo")
	_, err = repo.Get(charmURL)
	s.checkNotFoundErr(c, err, charmDir, charmURL)
}

func (s *charmPathRepoSuite) TestMissingRepo(c *gc.C) {
	charmDir := filepath.Join(s.repoPath, "mysql")
	s.cloneCharmDir(s.repoPath, "mysql")
	repo, err := charmrepo.NewCharmPath(charmDir)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(os.RemoveAll(s.repoPath), gc.IsNil)
	_, err = repo.Get(charm.MustParseURL("local:quantal/zebra"))
	c.Assert(err, gc.ErrorMatches, `no repository found at ".*"`)
}

func (s *charmPathRepoSuite) TestCharmArchive(c *gc.C) {
	charmDir := filepath.Join(s.repoPath, "mysql")
	s.cloneCharmDir(s.repoPath, "mysql")
	repo, err := charmrepo.NewCharmPath(charmDir)
	c.Assert(err, jc.ErrorIsNil)

	charmURL := charm.MustParseURL("local:quantal/mysql")
	ch, err := repo.Get(charmURL)
	c.Assert(err, gc.IsNil)
	c.Assert(ch.Revision(), gc.Equals, 1)
}

func (s *charmPathRepoSuite) TestMismatchedName(c *gc.C) {
	charmDir := filepath.Join(s.repoPath, "zebra")
	err := fs.Copy(filepath.Join(TestCharms.Path(), "quantal", "mysql"), charmDir)
	c.Assert(err, jc.ErrorIsNil)
	repo, err := charmrepo.NewCharmPath(charmDir)
	c.Assert(err, jc.ErrorIsNil)

	charmURL := charm.MustParseURL("local:quantal/zebra")
	_, err = repo.Get(charmURL)
	s.checkNotFoundErr(c, err, charmDir, charmURL)
}

func (s *charmPathRepoSuite) TestMismatchedRevision(c *gc.C) {
	charmDir := filepath.Join(s.repoPath, "mysql")
	s.cloneCharmDir(s.repoPath, "mysql")
	repo, err := charmrepo.NewCharmPath(charmDir)
	c.Assert(err, jc.ErrorIsNil)

	charmURL := charm.MustParseURL("local:quantal/mysql-2")
	_, err = repo.Get(charmURL)
	s.checkNotFoundErr(c, err, charmDir, charmURL)
}

func (s *charmPathRepoSuite) TestFindsSymlinks(c *gc.C) {
	realPath := TestCharms.ClonedDirPath(c.MkDir(), "dummy")
	charmsPath := c.MkDir()
	linkPath := filepath.Join(charmsPath, "dummy")
	err := os.Symlink(realPath, linkPath)
	c.Assert(err, gc.IsNil)

	repo, err := charmrepo.NewCharmPath(filepath.Join(charmsPath, "dummy"))
	c.Assert(err, jc.ErrorIsNil)
	ch, err := repo.Get(charm.MustParseURL("local:quantal/dummy"))
	c.Assert(err, gc.IsNil)
	c.Assert(ch.Revision(), gc.Equals, 1)
	c.Assert(ch.Meta().Name, gc.Equals, "dummy")
	c.Assert(ch.Config().Options["title"].Default, gc.Equals, "My Title")
	c.Assert(ch.(*charm.CharmDir).Path, gc.Equals, linkPath)
}

func (s *charmPathRepoSuite) TestResolve(c *gc.C) {
	// Define the tests to be run.
	tests := []struct {
		name   string
		series string
		url    string
		err    string
	}{{
		name:   "mysql",
		series: "quantal",
		url:    "local:quantal/mysql-1",
	}, {
		name:   "openstack",
		series: "bundle",
		url:    "local:bundle/openstack-0",
	}, {
		name:   "multi-series",
		series: "trusty",
		url:    "local:trusty/new-charm-with-multi-series-7",
	}, {
		name:   "multi-series",
		series: "",
		url:    "local:precise/new-charm-with-multi-series-7",
	}, {
		name:   "multi-series",
		series: "wily",
		err:    `series "wily" not supported by charm`,
	}, {
		name:   "riak",
		series: "quantal",
		url:    "local:quantal/riak-7",
	}, {
		name:   "riak",
		series: "",
		err:    "no series specified",
	}}

	// Run the tests.
	for i, test := range tests {
		msg := test.url
		if msg == "" {
			msg = test.err
		}
		c.Logf("test %d: %s", i, msg)
		sub := "quantal"
		if test.series == "bundle" {
			sub = test.series
		}
		charmOrBundleDir := filepath.Join(TestCharms.Path(), sub, test.name)
		repo, err := charmrepo.NewCharmPath(charmOrBundleDir)
		c.Assert(err, jc.ErrorIsNil)
		url, err := repo.Resolve(test.series)
		if test.err != "" {
			c.Assert(err, gc.ErrorMatches, test.err)
			c.Assert(url, gc.IsNil)
			continue
		}
		c.Assert(err, jc.ErrorIsNil)
		c.Assert(url, jc.DeepEquals, charm.MustParseURL(test.url))
	}
}

func (s *charmPathRepoSuite) TestGetBundle(c *gc.C) {
	charmOrBundleDir := filepath.Join(TestCharms.Path(), "bundle", "openstack")
	repo, err := charmrepo.NewCharmPath(charmOrBundleDir)
	c.Assert(err, jc.ErrorIsNil)
	url := charm.MustParseURL("local:bundle/openstack")
	b, err := repo.GetBundle(url)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(b.Data(), jc.DeepEquals, TestCharms.BundleDir("openstack").Data())
}

func (s *charmPathRepoSuite) TestGetBundleSymlink(c *gc.C) {
	realPath := TestCharms.ClonedBundleDirPath(c.MkDir(), "wordpress-simple")
	bundlesPath := c.MkDir()
	linkPath := filepath.Join(bundlesPath, "wordpress-simple")
	err := os.Symlink(realPath, linkPath)
	c.Assert(err, jc.ErrorIsNil)
	url := charm.MustParseURL("local:bundle/wordpress-simple")

	repo, err := charmrepo.NewCharmPath(filepath.Join(bundlesPath, "wordpress-simple"))
	c.Assert(err, jc.ErrorIsNil)
	b, err := repo.GetBundle(url)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(b.Data(), jc.DeepEquals, TestCharms.BundleDir("wordpress-simple").Data())
}

var invalidCharmURLTests = []struct {
	about  string
	bundle bool
	url    string
	err    string
}{{
	about: "get charm: non-local schema",
	url:   "cs:trusty/django-42",
	err:   `local charm path got URL with non-local schema: "cs:trusty/django-42"`,
}, {
	about:  "get bundle: non-local schema",
	bundle: true,
	url:    "cs:bundle/django-scalable",
	err:    `local charm path got URL with non-local schema: "cs:bundle/django-scalable"`,
}, {
	about: "get charm: bundle provided",
	url:   "local:bundle/rails",
	err:   `expected a charm URL, got bundle URL "local:bundle/rails"`,
}, {
	about:  "get bundle: charm provided",
	bundle: true,
	url:    "local:trusty/rails",
	err:    `expected a bundle URL, got charm URL "local:trusty/rails"`,
}}

func (s *charmPathRepoSuite) TestInvalidURLTest(c *gc.C) {
	charmDir := filepath.Join(s.repoPath, "mysql")
	s.cloneCharmDir(s.repoPath, "mysql")
	repo, err := charmrepo.NewCharmPath(charmDir)
	c.Assert(err, jc.ErrorIsNil)

	var e interface{}
	for i, test := range invalidCharmURLTests {
		c.Logf("test %d: %s", i, test.about)
		curl := charm.MustParseURL(test.url)
		if test.bundle {
			e, err = repo.GetBundle(curl)
		} else {
			e, err = repo.Get(curl)
		}
		c.Assert(e, gc.IsNil)
		c.Assert(err, gc.ErrorMatches, test.err)
	}
}
