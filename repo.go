// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

// Package charmrepo implements access to charm repositories.

package charmrepo // import "github.com/juju/charmrepo/v5"

import (
	"github.com/juju/charm/v8"
	"github.com/juju/loggo"
)

var logger = loggo.GetLogger("juju.charm.charmrepo")

// Interface represents a charm repository (a collection of charms).
type Interface interface {
	// Get reads the charm referenced by curl into a file
	// with the given path, which will be created if needed. Note that
	// the path's parent directory must already exist.
	Get(curl *charm.URL, archivePath string) (*charm.CharmArchive, error)

	// GetBundle returns the bundle referenced by curl.
	GetBundle(curl *charm.URL, archivePath string) (charm.Bundle, error)

	// Resolve resolves the given reference to a canonical form which refers
	// unambiguously to a specific revision of an entity. If the entity
	// is a charm that may support more than one series, canonRef.Series will
	// be empty and supportedSeries will hold the list of series supported by
	// the charm with the preferred series first.
	// If ref holds a series, then Resolve will always ensure that the returned
	// entity supports that series.
	Resolve(ref *charm.URL) (canonRef *charm.URL, supportedSeries []string, err error)
}
