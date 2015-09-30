// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo

import (
	"os"
	"path/filepath"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
)

// BundlePath is used to access a bundle at a given path.
type BundlePath interface {
	// Bundle returns the bundle represented by this path.
	Bundle() (charm.Bundle, *charm.URL)
}

// bundlePath represents a path (eg local directory) containing a
// single bundle.
type bundlePath struct {
	Name       string
	BundleInfo charm.Bundle
}

var _ BundlePath = (*bundlePath)(nil)

// NewBundlePath creates and returns an instance used to
// open a bundle located at the given path.
func NewBundlePath(path string) (BundlePath, error) {
	if path == "" {
		return nil, errgo.New("path to bundle not specified")
	}
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, errgo.Newf("path %q does not exist", path)
	}
	b, err := charm.ReadBundle(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errgo.Newf("no bundle found at %q", path)
		}
		return nil, err
	}
	_, name := filepath.Split(path)
	return &bundlePath{
		Name:       name,
		BundleInfo: b,
	}, nil
}

// Bundle is defined on BundlePath.
func (r *bundlePath) Bundle() (charm.Bundle, *charm.URL) {
	url := &charm.URL{
		Schema:   "local",
		Name:     r.Name,
		Series:   "bundle",
		Revision: 0,
	}
	return r.BundleInfo, url
}
