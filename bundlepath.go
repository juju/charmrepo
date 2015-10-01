// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo

import (
	"os"
	"path/filepath"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
)

// NewBundleAtPath creates and returns a bundle at a given path.
func NewBundleAtPath(path string) (charm.Bundle, *charm.URL, error) {
	if path == "" {
		return nil, nil, errgo.New("path to bundle not specified")
	}
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil, os.ErrNotExist
	}
	b, err := charm.ReadBundle(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, errgo.Newf("no bundle found at %q", path)
		}
		return nil, nil, err
	}
	_, name := filepath.Split(path)
	url := &charm.URL{
		Schema:   "local",
		Name:     name,
		Series:   "bundle",
		Revision: 0,
	}
	return b, url, nil
}
