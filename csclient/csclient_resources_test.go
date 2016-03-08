// Copyright 2016 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package csclient

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charm.v6-unstable/resource"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
)

type ResourceSuite struct{}

var _ = gc.Suite(ResourceSuite{})

func (ResourceSuite) TestListResources(c *gc.C) {
	data := "somedata"
	fp, err := resource.GenerateFingerprint(strings.NewReader(data))
	c.Assert(err, jc.ErrorIsNil)

	results := map[string][]params.Resource{
		"cs:quantal/starsay": []params.Resource{
			{
				Name:        "data",
				Type:        params.FileResource,
				Origin:      params.OriginStore,
				Path:        "data.zip",
				Description: "some zip file",
				Revision:    1,
				Fingerprint: fp.String(),
				Size:        int64(len(data)),
			},
		},
	}

	b, err := json.Marshal(results)
	c.Assert(err, jc.ErrorIsNil)

	f := &fakeClient{
		Stub: &testing.Stub{},
		ReturnDoWithBody: &http.Response{
			StatusCode: 200,
			Body:       ioutil.NopCloser(bytes.NewReader(b)),
		},
	}

	client := Client{bclient: f}
	url := charm.MustParseURL("cs:quantal/starsay")
	ret, err := client.ListResources([]*charm.URL{url})
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(ret, gc.DeepEquals, results)
}

func (ResourceSuite) TestUploadResource(c *gc.C) {
	data := []byte("boo!")
	reader := bytes.NewReader(data)

	result := params.ResourceUploadResponse{Revision: 1}
	b, err := json.Marshal(result)
	c.Assert(err, jc.ErrorIsNil)

	f := &fakeClient{
		Stub: &testing.Stub{},
		ReturnDoWithBody: &http.Response{
			StatusCode:    200,
			Body:          ioutil.NopCloser(bytes.NewReader(b)),
			ContentLength: int64(len(b)),
		},
	}

	client := Client{bclient: f}
	id := charm.MustParseURL("cs:quantal/starsay")
	rev, err := client.UploadResource(id, "resname", "data.zip", reader)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(rev, gc.Equals, 1)
	f.CheckCallNames(c, "DoWithBody")
	req := f.Calls()[0].Args[0].(*http.Request)
	body := f.Calls()[0].Args[1].(io.ReadSeeker)

	hash, size, err := readerHashAndSize(reader)
	c.Assert(err, jc.ErrorIsNil)

	c.Assert(req.URL.String(), gc.Equals, "/v5/quantal/starsay/resources/resname?hash="+hash+"&filename=data.zip")
	c.Assert(req.ContentLength, gc.Equals, size)
	c.Assert(body, gc.DeepEquals, reader)
}

func (ResourceSuite) TestGetResource(c *gc.C) {
	data := []byte("boo!")
	fp, err := resource.GenerateFingerprint(bytes.NewReader(data))
	c.Assert(err, jc.ErrorIsNil)
	body := ioutil.NopCloser(bytes.NewReader(data))

	resp := &http.Response{
		StatusCode: 200,
		Body:       body,
		Header: http.Header{
			params.ResourceRevisionHeader: []string{"1"},
			params.ContentHashHeader:      []string{fp.String()},
		},
		ContentLength: int64(len(data)),
	}

	f := &fakeClient{
		Stub:             &testing.Stub{},
		ReturnDoWithBody: resp,
	}

	client := Client{bclient: f}
	id := charm.MustParseURL("cs:quantal/starsay")
	resdata, err := client.GetResource(id, 1, "data")
	c.Assert(err, jc.ErrorIsNil)
	c.Check(resdata, gc.DeepEquals, ResourceData{
		ReadCloser: body,
		Revision:   1,
		Hash:       fp.String(),
		Size:       int64(len(data)),
	})
}

type fakeClient struct {
	*testing.Stub
	ReturnDoWithBody *http.Response
}

func (f *fakeClient) DoWithBody(req *http.Request, r io.ReadSeeker) (*http.Response, error) {
	f.AddCall("DoWithBody", req, r)
	return f.ReturnDoWithBody, f.NextErr()
}
