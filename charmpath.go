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

func isNotExistsError(err error) bool {
	if os.IsNotExist(err) {
		return true
	}
	// On Windows, we get a path error due to a GetFileAttributesEx syscall.
	// To avoid being too proscriptive, we'll simply check for the error
	// type and not any content.
	if _, ok := err.(*os.PathError); ok {
		return true
	}
	return false
}

func isValidCharmOrBundlePath(path string) bool {
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
	_, err := os.Stat(path)
	if isNotExistsError(err) {
		return nil, nil, os.ErrNotExist
	} else if err == nil && !isValidCharmOrBundlePath(path) {
		return nil, nil, InvalidPath(path)
	}
	ch, err := charm.ReadCharm(path)
	if err != nil {
		if isNotExistsError(err) {
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
