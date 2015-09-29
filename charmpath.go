// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo

import (
	"fmt"
	"os"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"path/filepath"
)

// charmPath represents a path (eg local directory) containing a
// single charm.
type charmPath struct {
	Path string
}

var _ Interface = (*charmPath)(nil)

// newCharmPath creates and returns a new local Juju charm repo
// located at the given local path.
func newCharmPath(path string) (Interface, error) {
	if path == "" {
		return nil, errgo.New("path to charm not specified")
	}
	if ok, err := pathContainsCharmOrBundle(path); !ok {
		return nil, err
	}
	return &charmPath{
		Path: path,
	}, nil
}

func pathContainsCharmOrBundle(path string) (bool, error) {
	_, err := charm.ReadBundle(path)
	if err == nil {
		return true, nil
	}
	_, err = charm.ReadCharm(path)
	if err != nil {
		return false, err
	}
	return true, nil
}

// Resolve implements Interface.Resolve.
// If series is specified, it is validated against the supported
// series declared by the charm.
func (r *charmPath) Resolve(series string) (*charm.URL, error) {
	if series == "bundle" {
		return r.resolveBundle()
	}
	ch, err := charm.ReadCharm(r.Path)
	if err != nil {
		return nil, err
	}
	meta := ch.Meta()
	supportedSeries := meta.SupportedSeries
	if series == "" {
		if len(supportedSeries) == 0 {
			return nil, errgo.Newf("no series specified")
		}
		series = supportedSeries[0]
	} else {
		meta := ch.Meta()
		if !validSeries(series, meta.SupportedSeries) {
			return nil, errgo.Newf("series %q not supported by charm", series)
		}
	}
	return &charm.URL{
		Schema:   "local",
		Name:     meta.Name,
		Series:   series,
		Revision: ch.Revision(),
	}, nil
}

func (r *charmPath) resolveBundle() (*charm.URL, error) {
	_, err := charm.ReadBundle(r.Path)
	if err != nil {
		return nil, err
	}
	_, last := filepath.Split(r.Path)
	// Bundles are named after their directory and have no revision.
	return &charm.URL{
		Schema:   "local",
		Name:     last,
		Series:   "bundle",
		Revision: 0,
	}, nil
}

func validSeries(series string, supportedSeries []string) bool {
	if series == "" || len(supportedSeries) == 0 {
		return true
	}
	for _, s := range supportedSeries {
		if s == series {
			return true
		}
	}
	return false
}

// Get reads the charm in this repo's path.
// The charm name and revision are validated against those
// supplied in the URL. If there is a mismatch, a not found
// error is returned.
func (r *charmPath) Get(curl *charm.URL) (charm.Charm, error) {
	if err := r.checkUrlAndPath(curl); err != nil {
		return nil, err
	}
	if curl.Series == "bundle" {
		return nil, errgo.Newf("expected a charm URL, got bundle URL %q", curl)
	}
	ch, err := charm.ReadCharm(r.Path)
	if err != nil {
		return nil, errgo.Newf("failed to load charm at %q: %s", r.Path, err)
	}
	if ch.Meta().Name != curl.Name || curl.Revision >= 0 && ch.Revision() != curl.Revision {
		return nil, entityNotFound(curl, r.Path)
	}
	meta := ch.Meta()
	if !validSeries(curl.Series, meta.SupportedSeries) {
		return nil, errgo.Newf("series %q not supported by charm", curl.Series)
	}
	return ch, nil
}

// GetBundle implements Interface.GetBundle.
func (r *charmPath) GetBundle(curl *charm.URL) (charm.Bundle, error) {
	if err := r.checkUrlAndPath(curl); err != nil {
		return nil, err
	}
	if curl.Series != "bundle" {
		return nil, errgo.Newf("expected a bundle URL, got charm URL %q", curl)
	}
	// Note that the bundle does not inherently own a name different than the
	// directory name. Neither the name nor the revision are included in the
	// bundle metadata.
	// TODO frankban: handle bundle revisions, totally ignored for now.
	return charm.ReadBundleDir(r.Path)
}

// checkUrlAndPath checks that the given URL represents a local entity and that
// the repository path exists.
func (r *charmPath) checkUrlAndPath(curl *charm.URL) error {
	// This is merely a sanity check - charm path repos always
	// use local schema.
	if curl.Schema != "local" {
		return fmt.Errorf("local charm path got URL with non-local schema: %q", curl)
	}
	info, err := os.Stat(r.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return repoNotFound(r.Path)
		}
		return err
	}
	if !info.IsDir() {
		return repoNotFound(r.Path)
	}
	return nil
}
