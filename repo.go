// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// Package charmrepo implements access to charm repositories.

package charmrepo

import (
	"fmt"

	"github.com/juju/loggo"
	"gopkg.in/juju/charm.v6-unstable"
)

var logger = loggo.GetLogger("juju.charm.charmrepo")

// Interface represents a charm repository (a collection of charms).
type Interface interface {
	// Get returns the charm referenced by curl.
	Get(curl *charm.URL) (charm.Charm, error)

	// GetBundle returns the bundle referenced by curl.
	GetBundle(curl *charm.URL) (charm.Bundle, error)

	// Resolve resolves revision of the given entity reference and
	// in addition, returns which series the entity may declare it
	// supports. If the series is not specified, the caller may pick
	// a series from those supported by the entity. If a series is
	// specified and it is not supported, an error is returned.
	// If the revision is not specified, it will be resolved to the
	// latest available revision for that entity, possibly accounting
	// for series if it is specified.
	// The second return value holds the list of possible series
	// supported by the entity, with the preferred series first.
	Resolve(ref *charm.Reference) (*charm.URL, []string, error)
}

// InferRepository returns a charm repository inferred from the provided charm
// or bundle reference.
// Charm store references will use the provided parameters.
// Local references will use the provided path.
func InferRepository(ref *charm.Reference, charmStoreParams NewCharmStoreParams, localRepoPath string) (Interface, error) {
	switch ref.Schema {
	case "cs":
		return NewCharmStore(charmStoreParams), nil
	case "local":
		return NewLocalRepository(localRepoPath)
	}
	// TODO fix this error message to reference bundles too?
	return nil, fmt.Errorf("unknown schema for charm reference %q", ref)
}
