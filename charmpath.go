// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
)

func mightBeCharmOrBundlePath(path string, info os.FileInfo) bool {
	if !info.IsDir() {
		return false
	}
	//Exclude relative paths.
	return strings.HasPrefix(path, ".") || filepath.IsAbs(path)
}

// NewCharmAtPath returns the charm represented by this path,
// and a URL that describes it. If the series is empty,
// the charm's default series is used, if any.
// Otherwise, the series is validated against those the
// charm declares it supports.
func NewCharmAtPath(path, series string) (charm.Charm, *charm.URL, error) {
	if path == "" {
		return nil, nil, errgo.New("empty charm path")
	}
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil, os.ErrNotExist
	}
	if !mightBeCharmOrBundlePath(path, fi) {
		return nil, nil, InvalidPath(path)
	}
	ch, err := charm.ReadCharm(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, CharmNotFound(path)
		}
		return nil, nil, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	_, name := filepath.Split(absPath)
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
