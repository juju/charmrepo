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

type bundlePathSuite struct {
	repoPath string
}

var _ = gc.Suite(&bundlePathSuite{})

func (s *bundlePathSuite) SetUpTest(c *gc.C) {
	s.repoPath = c.MkDir()
}

func (s *bundlePathSuite) cloneCharmDir(path, name string) string {
	return TestCharms.ClonedDirPath(path, name)
}

func (s *bundlePathSuite) TestNoPath(c *gc.C) {
	_, err := charmrepo.NewBundlePath("")
	c.Assert(err, gc.ErrorMatches, "path to bundle not specified")
}

func (s *bundlePathSuite) TestInvalidPath(c *gc.C) {
	_, err := charmrepo.NewBundlePath("foo")
	c.Assert(err, gc.ErrorMatches, `path "foo" does not exist`)
}

func (s *bundlePathSuite) TestNoBundleAtPath(c *gc.C) {
	_, err := charmrepo.NewBundlePath(c.MkDir())
	c.Assert(err, gc.ErrorMatches, `no bundle found at ".*"`)
}

func (s *bundlePathSuite) TestGetBundle(c *gc.C) {
	bundleDir := filepath.Join(TestCharms.Path(), "bundle", "openstack")
	path, err := charmrepo.NewBundlePath(bundleDir)
	c.Assert(err, jc.ErrorIsNil)
	b, url := path.Bundle()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(b.Data(), jc.DeepEquals, TestCharms.BundleDir("openstack").Data())
	c.Assert(url, gc.DeepEquals, charm.MustParseURL("local:bundle/openstack-0"))
}

func (s *bundlePathSuite) TestGetBundleSymlink(c *gc.C) {
	realPath := TestCharms.ClonedBundleDirPath(c.MkDir(), "wordpress-simple")
	bundlesPath := c.MkDir()
	linkPath := filepath.Join(bundlesPath, "wordpress-simple")
	err := os.Symlink(realPath, linkPath)
	c.Assert(err, jc.ErrorIsNil)
	url := charm.MustParseURL("local:bundle/wordpress-simple")

	path, err := charmrepo.NewBundlePath(filepath.Join(bundlesPath, "wordpress-simple"))
	c.Assert(err, jc.ErrorIsNil)
	b, url := path.Bundle()
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(b.Data(), jc.DeepEquals, TestCharms.BundleDir("wordpress-simple").Data())
	c.Assert(url, gc.DeepEquals, charm.MustParseURL("local:bundle/wordpress-simple-0"))
}