// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package params_test

import (
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable/resource"

	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
)

const fingerprint = "123456789012345678901234567890123456789012345678"

type HelpersSuite struct {
	testing.IsolationSuite
}

var _ = gc.Suite(&HelpersSuite{})

func (HelpersSuite) TestResource2API(c *gc.C) {
	fp, err := resource.NewFingerprint([]byte(fingerprint))
	c.Assert(err, jc.ErrorIsNil)
	res := resource.Resource{
		Meta: resource.Meta{
			Name:        "spam",
			Type:        resource.TypeFile,
			Path:        "spam.tgz",
			Description: "you need it",
		},
		Origin:      resource.OriginUpload,
		Revision:    1,
		Fingerprint: fp,
		Size:        10,
	}
	err = res.Validate()
	c.Assert(err, jc.ErrorIsNil)
	apiInfo := params.Resource2API(res)

	c.Check(apiInfo, jc.DeepEquals, params.Resource{
		Name:        "spam",
		Type:        "file",
		Path:        "spam.tgz",
		Description: "you need it",
		Origin:      "upload",
		Revision:    1,
		Fingerprint: []byte(fingerprint),
		Size:        10,
	})
}

func (HelpersSuite) TestAPI2Resource(c *gc.C) {
	res, err := params.API2Resource(params.Resource{
		Name:        "spam",
		Type:        "file",
		Path:        "spam.tgz",
		Description: "you need it",
		Origin:      "upload",
		Revision:    1,
		Fingerprint: []byte(fingerprint),
		Size:        10,
	})
	c.Assert(err, jc.ErrorIsNil)

	fp, err := resource.NewFingerprint([]byte(fingerprint))
	c.Assert(err, jc.ErrorIsNil)
	expected := resource.Resource{
		Meta: resource.Meta{
			Name:        "spam",
			Type:        resource.TypeFile,
			Path:        "spam.tgz",
			Description: "you need it",
		},
		Origin:      resource.OriginUpload,
		Revision:    1,
		Fingerprint: fp,
		Size:        10,
	}
	err = expected.Validate()
	c.Assert(err, jc.ErrorIsNil)

	c.Check(res, jc.DeepEquals, expected)
}
