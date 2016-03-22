// Copyright 2016 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package csclient

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"mime/multipart"
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
				Type:        "file",
				Origin:      "store",
				Path:        "data.zip",
				Description: "some zip file",
				Revision:    1,
				Fingerprint: fp.Bytes(),
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
	res := params.Resource{
		Name:        "name",
		Type:        "file",
		Path:        "foo.tgz",
		Description: "foobar",
		Origin:      "store",
		Revision:    5,
		Fingerprint: fp.Bytes(),
	}

	b := &bytes.Buffer{}
	multi := multipart.NewWriter(b)
	resBytes, err := json.Marshal(res)
	c.Assert(err, jc.ErrorIsNil)
	w, err := multi.CreatePart(nil)
	c.Assert(err, jc.ErrorIsNil)
	_, err = w.Write(resBytes)
	c.Assert(err, jc.ErrorIsNil)
	w, err = multi.CreateFormFile("data", "foo.tgz")
	c.Assert(err, jc.ErrorIsNil)
	_, err = w.Write(data)
	err = multi.Close()
	c.Assert(err, jc.ErrorIsNil)

	resp := &http.Response{
		StatusCode: 200,
		Body:       ioutil.NopCloser(bytes.NewReader(b.Bytes())),
		Header: http.Header{
			"Content-Type": []string{multi.FormDataContentType()},
		},
		ContentLength: int64(b.Len()),
	}

	f := &fakeClient{
		Stub:             &testing.Stub{},
		ReturnDoWithBody: resp,
	}

	client := Client{bclient: f}
	id := charm.MustParseURL("cs:quantal/starsay")
	resdata, err := client.GetResource(id, 1, "data")
	c.Assert(err, jc.ErrorIsNil)
	c.Check(resdata.Resource, gc.DeepEquals, res)
	bytes, err := ioutil.ReadAll(resdata.ReadCloser)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(bytes, gc.DeepEquals, data)
}

type fakeClient struct {
	*testing.Stub
	ReturnDoWithBody *http.Response
}

func (f *fakeClient) DoWithBody(req *http.Request, r io.ReadSeeker) (*http.Response, error) {
	f.AddCall("DoWithBody", req, r)
	return f.ReturnDoWithBody, f.NextErr()
}
