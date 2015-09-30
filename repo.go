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

	// Resolve resolves the series and revision of the given entity
	// reference. If the series is not specified, it may be resolved
	// by the charm store or rejected. After the series is resolved,
	// if the revision is not specified, it will be resolved to the latest
	// available revision for that series.
	// The second return value holds the list of possible series
	// supported by the entity, with the preferred series first.
	Resolve(ref *charm.Reference) (*charm.URL, []string, error)
}

// Latest returns the latest revision of the charm referenced by curl, regardless
// of the revision set on each curl.
// This is a helper which calls the bulk method and unpacks a single result.
func Latest(repo *CharmStore, curl *charm.URL) (int, error) {
	revs, err := repo.Latest(curl)
	if err != nil {
		return 0, err
	}
	if len(revs) != 1 {
		return 0, fmt.Errorf("expected 1 result, got %d", len(revs))
	}
	rev := revs[0]
	if rev.Err != nil {
		return 0, rev.Err
	}
	return rev.Revision, nil
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
