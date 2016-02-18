// Copyright 2016 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package csclient

import (
	"io"

	"github.com/juju/errors"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charm.v6-unstable/resource"
)

// resourcesClient provides the Client methods related to resources.
type resourcesClient struct{}

// ListResources composes, for each of the identified charms, the
// list of details for each of the charm's resources. Those details
// are those associated with the specific charm revision. They
// include the resource's metadata and revision.
func (client resourcesClient) ListResources(ids []*charm.URL) ([][]resource.Resource, error) {
	resources := make([][]resource.Resource, len(ids))
	// TODO(ericsnow) We simulate the charm store behavior here until
	// the charm store actually supports resources. Since no charms
	// have resources the result is always an empty list for each charm.
	//
	// Once the charmstore supports resources, this must be implemented
	// and tests added.
	return resources, nil
}

// GetResource returns a reader for the resource's data. That data
// is streamed from the charm store. The charm's revision, if any,
// is ignored. If the identified resource is not in the charm store
// then errors.NotFound is returned.
func (client resourcesClient) GetResource(id *charm.URL, resourceName string, revision int) (io.ReadCloser, error) {
	// TODO(ericsnow) We simulate the charm store behavior here until
	// the charm store actually supports resources. Since no charms
	// have resources the result is always a "not found" error.
	//
	// Once the charmstore supports resources, this must be implemented
	// and tests added.
	return nil, errors.NotFoundf("resource %q", resourceName)
}
