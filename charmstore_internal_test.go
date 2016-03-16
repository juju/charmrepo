// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charm.v6-unstable/resource"

	"gopkg.in/juju/charmrepo.v2-unstable/csclient"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
)

type ResourceSuite struct{}

var _ = gc.Suite(ResourceSuite{})

func (ResourceSuite) TestListResources(c *gc.C) {
	data := "somedata"
	fp, err := resource.GenerateFingerprint(strings.NewReader(data))

	f := &fakeClient{
		Stub: &testing.Stub{},
		ReturnListResources: map[string][]params.Resource{
			"cs:quantal/starsay": []params.Resource{
				{
					Name:        "data",
					Type:        "file",
					Origin:      "store",
					Path:        "data.zip",
					Description: "some zip file",
					Revision:    1,
					Fingerprint: fp.Bytes(),
					Size:        int64(len(data)),
				},
			},
		},
	}

	s := &CharmStore{client: f}

	url := charm.MustParseURL("cs:quantal/starsay")
	missing := charm.MustParseURL("cs:quantal/notexist")
	res, err := s.ListResources([]*charm.URL{missing, url})
	c.Assert(err, jc.ErrorIsNil)
	f.CheckCall(c, 0, "ListResources", []*charm.URL{missing, url})
	c.Assert(res, gc.DeepEquals, []ResourceResult{
		{
			Err: CharmNotFound(missing.String()),
		},
		{
			Resources: []resource.Resource{
				{
					Meta: resource.Meta{
						Name:        "data",
						Type:        resource.TypeFile,
						Path:        "data.zip",
						Description: "some zip file",
					},
					Origin:      resource.OriginStore,
					Revision:    1,
					Fingerprint: fp,
					Size:        int64(len(data)),
				},
			},
		},
	})
}

func (ResourceSuite) TestUploadResources(c *gc.C) {
	f := &fakeClient{
		Stub:                 &testing.Stub{},
		ReturnUploadResource: 1,
	}
	path := filepath.Join(c.MkDir(), "foo.zip")
	err := ioutil.WriteFile(path, []byte("data"), 0600)
	c.Assert(err, jc.ErrorIsNil)
	s := &CharmStore{client: f}
	id := charm.MustParseURL("cs:quantal/starsay")
	rev, err := s.UploadResource(id, "data", path)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(rev, gc.Equals, 1)
	f.CheckCall(c, 0, "UploadResource", id, "data", path, path)
}

func (ResourceSuite) TestUploadMissingResource(c *gc.C) {
	f := &fakeClient{Stub: &testing.Stub{}}
	s := &CharmStore{client: f}
	path := filepath.Join(c.MkDir(), "doesntexist")
	id := charm.MustParseURL("cs:quantal/starsay")
	_, err := s.UploadResource(id, "data", path)
	c.Assert(errgo.Cause(err), jc.Satisfies, os.IsNotExist)
	f.CheckNoCalls(c)
}

func (ResourceSuite) TestGetResource(c *gc.C) {
	data := "somedata"
	fp, err := resource.GenerateFingerprint(strings.NewReader(data))
	c.Assert(err, jc.ErrorIsNil)

	csData := csclient.ResourceData{
		ReadCloser: &fakeReadCloser{Buffer: bytes.NewBufferString(data)},
		Revision:   1,
		Hash:       fp.String(),
		Size:       int64(len(data)),
	}

	f := &fakeClient{
		Stub:              &testing.Stub{},
		ReturnGetResource: csData,
	}
	s := &CharmStore{client: f}
	id := charm.MustParseURL("cs:quantal/starsay")

	rd, err := s.GetResource(id, 1, "data")
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(rd, gc.DeepEquals, ResourceData{
		ReadCloser:  csData.ReadCloser,
		Size:        csData.Size,
		Revision:    csData.Revision,
		Fingerprint: fp,
	})
	f.CheckCall(c, 0, "GetResource", id, 1, "data")
}

func (ResourceSuite) TestGetResourceBadFp(c *gc.C) {
	data := "somedata"
	stub := &testing.Stub{}
	csData := csclient.ResourceData{
		ReadCloser: &fakeReadCloser{
			Stub:   stub,
			Buffer: bytes.NewBufferString(data),
		},
		Revision: 1,
		Hash:     "not a valid hash",
		Size:     int64(len(data)),
	}

	f := &fakeClient{
		Stub:              stub,
		ReturnGetResource: csData,
	}
	s := &CharmStore{client: f}
	id := charm.MustParseURL("cs:quantal/starsay")

	_, err := s.GetResource(id, 1, "data")
	c.Assert(err, gc.ErrorMatches, "invalid fingerprint returned from server.*")

	f.CheckCall(c, 0, "GetResource", id, 1, "data")
	// this is the close on the ReadCloser, which should get closed inside
	// GetResource, since we're returning with an error.
	f.CheckCall(c, 1, "Close")
}

func (ResourceSuite) TestPublish(c *gc.C) {
	f := &fakeClient{Stub: &testing.Stub{}}
	id := charm.MustParseURL("cs:quantal/starsay")
	idrev := id.WithRevision(5)
	idrev2 := id.WithRevision(6)
	f.ReturnPutWithResponse = params.PublishResponse{
		Id:            idrev,
		PromulgatedId: idrev2,
	}
	s := &CharmStore{client: f}
	resources := map[string]int{
		"data": 1,
		"lib":  2,
	}
	err := s.Publish(id, []string{"development"}, resources)
	c.Assert(err, jc.ErrorIsNil)
	f.CheckCall(c, 0, "Put", "/"+id.Path()+"/publish", &params.PublishRequest{
		Resources: resources,
		Channels:  []params.Channel{params.DevelopmentChannel},
	})
}

type fakeClient struct {
	apiClient
	*testing.Stub

	ReturnListResources   map[string][]params.Resource
	ReturnUploadResource  int
	ReturnGetResource     csclient.ResourceData
	ReturnPutWithResponse params.PublishResponse
}

func (f *fakeClient) ListResources(ids []*charm.URL) (map[string][]params.Resource, error) {
	f.AddCall("ListResources", ids)
	return f.ReturnListResources, nil
}

func (f *fakeClient) UploadResource(id *charm.URL, name, path string, r io.ReadSeeker) (revision int, err error) {
	file := r.(*os.File)
	f.AddCall("UploadResource", id, name, path, file.Name())
	return f.ReturnUploadResource, nil
}

func (f *fakeClient) GetResource(id *charm.URL, revision int, name string) (csclient.ResourceData, error) {
	f.AddCall("GetResource", id, revision, name)
	return f.ReturnGetResource, f.NextErr()
}

func (f *fakeClient) Put(path string, val interface{}) error {
	f.AddCall("Put", path, val)
	return f.NextErr()
}

type fakeReadCloser struct {
	*bytes.Buffer
	*testing.Stub
}

func (f *fakeReadCloser) Close() error {
	f.AddCall("Close")
	return f.NextErr()
}
