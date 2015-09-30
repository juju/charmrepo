// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo_test

import (
	"os"
	"path/filepath"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"

	"gopkg.in/juju/charmrepo.v2-unstable"
)

type charmPathSuite struct {
	repoPath string
}

var _ = gc.Suite(&charmPathSuite{})

func (s *charmPathSuite) SetUpTest(c *gc.C) {
	s.repoPath = c.MkDir()
}

func (s *charmPathSuite) cloneCharmDir(path, name string) string {
	return TestCharms.ClonedDirPath(path, name)
}

func (s *charmPathSuite) TestNoPath(c *gc.C) {
	_, err := charmrepo.NewCharmPath("")
	c.Assert(err, gc.ErrorMatches, "path to charm not specified")
}

func (s *charmPathSuite) TestInvalidPath(c *gc.C) {
	_, err := charmrepo.NewCharmPath("foo")
	c.Assert(err, gc.ErrorMatches, `path "foo" does not exist`)
}

func (s *charmPathSuite) TestNoCharmAtPath(c *gc.C) {
	_, err := charmrepo.NewCharmPath(c.MkDir())
	c.Assert(err, gc.ErrorMatches, "charm not found.*")
}

func (s *charmPathSuite) TestCharm(c *gc.C) {
	charmDir := filepath.Join(s.repoPath, "mysql")
	s.cloneCharmDir(s.repoPath, "mysql")
	path, err := charmrepo.NewCharmPath(charmDir)
	c.Assert(err, jc.ErrorIsNil)

	ch, url, err := path.Charm("quantal")
	c.Assert(err, gc.IsNil)
	c.Assert(ch.Meta().Name, gc.Equals, "mysql")
	c.Assert(ch.Revision(), gc.Equals, 1)
	c.Assert(url, gc.DeepEquals, charm.MustParseURL("local:quantal/mysql-1"))
}

func (s *charmPathSuite) TestNoSeriesSpecified(c *gc.C) {
	charmDir := filepath.Join(s.repoPath, "mysql")
	s.cloneCharmDir(s.repoPath, "mysql")
	path, err := charmrepo.NewCharmPath(charmDir)
	c.Assert(err, jc.ErrorIsNil)

	_, _, err = path.Charm("")
	c.Assert(err, gc.ErrorMatches, "series not specified and charm does not define any")
}

func (s *charmPathSuite) TestMuliSeriesDefault(c *gc.C) {
	charmDir := filepath.Join(s.repoPath, "multi-series")
	s.cloneCharmDir(s.repoPath, "multi-series")
	path, err := charmrepo.NewCharmPath(charmDir)
	c.Assert(err, jc.ErrorIsNil)

	ch, url, err := path.Charm("")
	c.Assert(err, gc.IsNil)
	c.Assert(ch.Meta().Name, gc.Equals, "new-charm-with-multi-series")
	c.Assert(ch.Revision(), gc.Equals, 7)
	c.Assert(url, gc.DeepEquals, charm.MustParseURL("local:precise/multi-series-7"))
}

func (s *charmPathSuite) TestMuliSeries(c *gc.C) {
	charmDir := filepath.Join(s.repoPath, "multi-series")
	s.cloneCharmDir(s.repoPath, "multi-series")
	path, err := charmrepo.NewCharmPath(charmDir)
	c.Assert(err, jc.ErrorIsNil)

	ch, url, err := path.Charm("trusty")
	c.Assert(err, gc.IsNil)
	c.Assert(ch.Meta().Name, gc.Equals, "new-charm-with-multi-series")
	c.Assert(ch.Revision(), gc.Equals, 7)
	c.Assert(url, gc.DeepEquals, charm.MustParseURL("local:trusty/multi-series-7"))
}

func (s *charmPathSuite) TestUnsupportedSeries(c *gc.C) {
	charmDir := filepath.Join(s.repoPath, "multi-series")
	s.cloneCharmDir(s.repoPath, "multi-series")
	path, err := charmrepo.NewCharmPath(charmDir)
	c.Assert(err, jc.ErrorIsNil)

	_, _, err = path.Charm("wily")
	c.Assert(err, gc.ErrorMatches, `series "wily" not supported by charm, supported series are.*`)
}

func (s *charmPathSuite) TestFindsSymlinks(c *gc.C) {
	realPath := TestCharms.ClonedDirPath(c.MkDir(), "dummy")
	charmsPath := c.MkDir()
	linkPath := filepath.Join(charmsPath, "dummy")
	err := os.Symlink(realPath, linkPath)
	c.Assert(err, gc.IsNil)

	path, err := charmrepo.NewCharmPath(filepath.Join(charmsPath, "dummy"))
	c.Assert(err, jc.ErrorIsNil)
	ch, url, err := path.Charm("quantal")
	c.Assert(err, gc.IsNil)
	c.Assert(ch.Revision(), gc.Equals, 1)
	c.Assert(ch.Meta().Name, gc.Equals, "dummy")
	c.Assert(ch.Config().Options["title"].Default, gc.Equals, "My Title")
	c.Assert(ch.(*charm.CharmDir).Path, gc.Equals, linkPath)
	c.Assert(url, gc.DeepEquals, charm.MustParseURL("local:quantal/dummy-1"))
}
