// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo

import (
	"os"
	"path/filepath"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
)

// NewCharmAtPath returns the charm represented by this path.
// If the series is not specified, the charm's default
// series is used, if any. Otherwise, the series is
// validated against those the charm declares it supports.
func NewCharmAtPath(path, series string) (charm.Charm, *charm.URL, error) {
	if path == "" {
		return nil, nil, errgo.New("path to charm not specified")
	}
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil, errgo.Newf("path %q does not exist", path)
	}
	ch, err := charm.ReadCharm(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, CharmNotFound(path)
		}
		return nil, nil, err
	}
	_, name := filepath.Split(path)
	meta := ch.Meta()
	seriesToUse, err := charm.SeriesForCharm(series, meta.Series)
	if err != nil {
		return nil, nil, err
	}
	url := &charm.URL{
		Schema:   "local",
		Name:     name,
		Series:   seriesToUse,
		Revision: ch.Revision(),
	}
	return ch, url, nil
}
