// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package params

import (
	"gopkg.in/juju/charm.v6-unstable/resource"
)

// Resource2API converts a charm resource into an API Resource struct.
func Resource2API(res resource.Resource) Resource {
	return Resource{
		Name:        res.Name,
		Type:        res.Type.String(),
		Path:        res.Path,
		Description: res.Description,
		Origin:      res.Origin.String(),
		Revision:    res.Revision,
		Fingerprint: res.Fingerprint.Bytes(),
		Size:        res.Size,
	}
}

// API2Resource converts an API Resource struct into
// a charm resource.
func API2Resource(apiInfo Resource) (resource.Resource, error) {
	var res resource.Resource

	rtype, err := resource.ParseType(apiInfo.Type)
	if err != nil {
		return res, err
	}

	origin, err := resource.ParseOrigin(apiInfo.Origin)
	if err != nil {
		return res, err
	}

	fp, err := deserializeFingerprint(apiInfo.Fingerprint)
	if err != nil {
		return res, err
	}

	res = resource.Resource{
		Meta: resource.Meta{
			Name:        apiInfo.Name,
			Type:        rtype,
			Path:        apiInfo.Path,
			Description: apiInfo.Description,
		},
		Origin:      origin,
		Revision:    apiInfo.Revision,
		Fingerprint: fp,
		Size:        apiInfo.Size,
	}

	if err := res.Validate(); err != nil {
		return res, err
	}
	return res, nil
}

// deserializeFingerprint converts the serialized fingerprint back into
// a Fingerprint. "zero" values are treated appropriately.
func deserializeFingerprint(fpSum []byte) (resource.Fingerprint, error) {
	if len(fpSum) == 0 {
		return resource.Fingerprint{}, nil
	}
	return resource.NewFingerprint(fpSum)
}
