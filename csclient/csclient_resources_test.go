// Copyright 2016 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package csclient

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charm.v6-unstable/resource"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/macaroon.v1"

	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
)

type ResourceSuite struct{}

var _ = gc.Suite(ResourceSuite{})

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

	c.Assert(req.URL.String(), gc.Equals, "/v5/quantal/starsay/resource/resname?hash="+hash+"&filename=data.zip")
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
			params.ContentHashHeader: []string{fp.String()},
		},
		ContentLength: int64(len(data)),
	}

	f := &fakeClient{
		Stub:             &testing.Stub{},
		ReturnDoWithBody: resp,
	}

	client := Client{bclient: f}
	id := charm.MustParseURL("cs:quantal/starsay")
	resdata, err := client.GetResource(id, "data", 1)
	c.Assert(err, jc.ErrorIsNil)
	c.Check(resdata, gc.DeepEquals, ResourceData{
		ReadCloser: body,
		Hash:       fp.String(),
		Size:       int64(len(data)),
	})
}

func (ResourceSuite) TestResourceMeta(c *gc.C) {
	data := "somedata"
	fp, err := resource.GenerateFingerprint(strings.NewReader(data))
	c.Assert(err, jc.ErrorIsNil)
	result := params.Resource{
		Name:        "data",
		Type:        "file",
		Origin:      "store",
		Path:        "data.zip",
		Description: "some zip file",
		Revision:    1,
		Fingerprint: fp.Bytes(),
		Size:        int64(len(data)),
	}

	b, err := json.Marshal(result)
	c.Assert(err, jc.ErrorIsNil)

	f := &fakeClient{
		Stub: &testing.Stub{},
		ReturnDoWithBody: &http.Response{
			StatusCode: 200,
			Body:       ioutil.NopCloser(bytes.NewReader(b)),
		},
	}

	client := Client{bclient: f}
	id := charm.MustParseURL("cs:quantal/starsay")
	resdata, err := client.ResourceMeta(id, "data", 1)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(resdata, gc.DeepEquals, result)
}

type InternalSuite struct{}

var _ = gc.Suite(InternalSuite{})

func (s InternalSuite) TestMacaroon(c *gc.C) {
	var m macaroon.Macaroon
	macs := macaroon.Slice{&m}
	client := New(Params{
		URL:  "https://foo.com",
		Auth: macs,
	})
	u, err := url.Parse("https://foo.com")
	c.Assert(err, jc.ErrorIsNil)
	bc := client.bclient.(*httpbakery.Client)
	cookies := bc.Jar.Cookies(u)
	expected, err := httpbakery.NewCookie(macs)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(cookies, gc.DeepEquals, []*http.Cookie{expected})
}

type fakeClient struct {
	*testing.Stub
	ReturnDoWithBody *http.Response
}

func (f *fakeClient) DoWithBody(req *http.Request, r io.ReadSeeker) (*http.Response, error) {
	f.AddCall("DoWithBody", req, r)
	return f.ReturnDoWithBody, f.NextErr()
}
