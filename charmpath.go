// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo

import (
	"os"
	"path/filepath"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
)

// CharmPath is used to access a charm at a given path.
type CharmPath interface {
	// Charm returns the charm represented by this path.
	// If the series is not specified, the charm's default
	// series is used, if any. Otherwise, the series is
	// validated against those the charm declares it supports.
	Charm(series string) (charm.Charm, *charm.URL, error)
}

// charmPath represents a path (eg local directory) containing a
// single charm.
type charmPath struct {
	Name      string
	CharmInfo charm.Charm
}

var _ CharmPath = (*charmPath)(nil)

// NewCharmPath creates and returns an instance used to
// open a charm located at the given path.
func NewCharmPath(path string) (CharmPath, error) {
	if path == "" {
		return nil, errgo.New("path to charm not specified")
	}
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, errgo.Newf("path %q does not exist", path)
	}
	ch, err := charm.ReadCharm(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errgo.Newf("no charm found at %q", path)
		}
		return nil, err
	}
	_, name := filepath.Split(path)
	return &charmPath{
		Name:      name,
		CharmInfo: ch,
	}, nil
}

// Charm is defined on CharmPath.
func (r *charmPath) Charm(series string) (charm.Charm, *charm.URL, error) {
	meta := r.CharmInfo.Meta()
	if series == "" && len(meta.Series) == 0 {
		return nil, nil, errgo.Newf("series not specified and charm does not define any")
	}
	seriesToUse, err := charm.SeriesForCharm(series, meta.Series)
	if err != nil {
		return nil, nil, err
	}
	url := &charm.URL{
		Schema:   "local",
		Name:     r.Name,
		Series:   seriesToUse,
		Revision: r.CharmInfo.Revision(),
	}
	return r.CharmInfo, url, nil
}
