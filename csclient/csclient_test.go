// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package csclient_test // import "gopkg.in/juju/charmrepo.v4/csclient"

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	neturl "net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6"
	"gopkg.in/juju/charm.v6/resource"
	"gopkg.in/juju/charmstore.v5"
	"gopkg.in/juju/idmclient.v1/idmtest"
	httpbakery2u "gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/bakerytest"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
	"gopkg.in/macaroon.v2"
	"gopkg.in/mgo.v2"

	"gopkg.in/juju/charmrepo.v4/csclient"
	"gopkg.in/juju/charmrepo.v4/csclient/params"
	charmtesting "gopkg.in/juju/charmrepo.v4/testing"
)

var charmRepo = charmtesting.NewRepo("../internal/test-charm-repo", "quantal")

// Define fake attributes to be used in tests.
var fakeContent, fakeHash, fakeSize = func() (string, string, int64) {
	content := "fake content"
	h := sha512.New384()
	h.Write([]byte(content))
	return content, fmt.Sprintf("%x", h.Sum(nil)), int64(len(content))
}()

type suite struct {
	jujutesting.IsolatedMgoSuite
	client       *csclient.Client
	srv          *httptest.Server
	handler      charmstore.HTTPCloseHandler
	serverParams charmstore.ServerParams
	identitySrv  *idmtest.Server
	termsSrv     *bakerytest.Discharger
	// termsChecker holds the third party caveat checker used
	// by termsSrv.
	termsChecker *termsChecker
}

var _ = gc.Suite(&suite{})

func (s *suite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.startServer(c, s.Session)
	s.client = csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     s.serverParams.AuthUsername,
		Password: s.serverParams.AuthPassword,
	})
}

func (s *suite) TearDownTest(c *gc.C) {
	s.srv.Close()
	s.handler.Close()
	s.identitySrv.Close()
	s.termsSrv.Close()
	s.IsolatedMgoSuite.TearDownTest(c)
}

func (s *suite) startServer(c *gc.C, session *mgo.Session) {
	s.identitySrv = idmtest.NewServer()

	s.termsChecker = &termsChecker{}
	s.termsSrv = bakerytest.NewDischarger(nil)
	s.termsSrv.CheckerP = s.termsChecker

	serverParams := charmstore.ServerParams{
		AuthUsername:          "test-user",
		AuthPassword:          "test-password",
		IdentityLocation:      s.identitySrv.URL.String(),
		TermsLocation:         s.termsSrv.Location(),
		MinUploadPartSize:     10,
		MaxUploadPartSize:     200,
		PublicKeyLocator:      httpbakery2u.NewPublicKeyRing(nil, nil),
		DockerRegistryAddress: "0.1.2.3",
	}
	c.Logf("identity location: %s; terms location %s", serverParams.IdentityLocation, serverParams.TermsLocation)

	db := session.DB("charmstore")
	handler, err := charmstore.NewServer(db, nil, "", serverParams, charmstore.V5)
	c.Assert(err, gc.IsNil)
	s.handler = handler
	s.srv = httptest.NewServer(handler)
	s.serverParams = serverParams
}

func (s *suite) TestNewWithBakeryClient(c *gc.C) {
	// Make a csclient.Client with a custom bakery client that
	// enables us to tell if that's really being used.
	used := false
	bclient := httpbakery.NewClient()
	bclient.Client = httpbakery.NewHTTPClient()
	bclient.Client.Transport = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		used = true
		return http.DefaultTransport.RoundTrip(req)
	})
	client := csclient.New(csclient.Params{
		URL:          s.srv.URL,
		BakeryClient: bclient,
	})
	s.identitySrv.SetDefaultUser("bob")
	err := client.UploadCharmWithRevision(
		charm.MustParseURL("~bob/precise/wordpress-0"),
		charmRepo.CharmDir("wordpress"),
		42,
	)
	c.Assert(err, gc.IsNil)
	c.Assert(used, gc.Equals, true)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (s *suite) TestIsAuthorizationError(c *gc.C) {
	bclient := httpbakery.NewClient()
	client := csclient.New(csclient.Params{
		URL:          s.srv.URL,
		BakeryClient: bclient,
	})
	doSomething := func() error {
		// Make a request that requires a discharge, which will be denied.
		err := client.UploadCharmWithRevision(
			charm.MustParseURL("~bob/precise/wordpress-0"),
			charmRepo.CharmDir("wordpress"),
			42,
		)
		return errgo.Mask(err, errgo.Any)
	}
	err := doSomething()
	c.Assert(err, gc.ErrorMatches, `cannot log in: cannot retrieve the authentication macaroon: cannot get discharge from "https://.*": cannot start interactive session: interaction required but not possible`)
	c.Assert(err, jc.Satisfies, csclient.IsAuthorizationError, gc.Commentf("cause type %T", errgo.Cause(err)))

	// TODO it might be nice to test the case where the discharge request returns an error
	// rather than an interaction-required error, but it's a bit awkward to do and probably
	// not that important or error-prone a path to test, so we don't for now.

	// Make a request that is denied because it's with the wrong user.
	s.identitySrv.SetDefaultUser("alice")
	err = doSomething()
	c.Assert(err, gc.ErrorMatches, `cannot post archive: access denied for user "alice"`)
	c.Assert(err, jc.Satisfies, csclient.IsAuthorizationError)

	err = &params.Error{
		Message: "hello",
		Code:    params.ErrForbidden,
	}
	c.Assert(err, gc.Not(jc.Satisfies), csclient.IsAuthorizationError)
}

func (s *suite) TestDefaultServerURL(c *gc.C) {
	// Add a charm used for tests.
	url := charm.MustParseURL("~charmers/vivid/testing-wordpress-42")
	err := s.client.UploadCharmWithRevision(
		url,
		charmRepo.CharmDir("wordpress"),
		42,
	)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, url)

	// Patch the default server URL.
	s.PatchValue(&csclient.ServerURL, s.srv.URL)

	// Instantiate a client using the default server URL.
	client := csclient.New(csclient.Params{
		User:     s.serverParams.AuthUsername,
		Password: s.serverParams.AuthPassword,
	})
	c.Assert(client.ServerURL(), gc.Equals, s.srv.URL)

	// Check that the request succeeds.
	err = client.Get("/vivid/testing-wordpress-42/expand-id", nil)
	c.Assert(err, gc.IsNil)
}

func (s *suite) TestSetHTTPHeader(c *gc.C) {
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		header = req.Header
	}))
	defer srv.Close()

	sendRequest := func(client *csclient.Client) {
		req, err := http.NewRequest("GET", "", nil)
		c.Assert(err, jc.ErrorIsNil)
		_, err = client.Do(req, "/")
		c.Assert(err, jc.ErrorIsNil)
	}
	client := csclient.New(csclient.Params{
		URL: srv.URL,
	})

	// Make a first request without custom headers.
	sendRequest(client)
	defaultHeaderLen := len(header)

	// Make a second request adding a couple of custom headers.
	h := make(http.Header)
	h.Set("k1", "v1")
	h.Add("k2", "v2")
	h.Add("k2", "v3")
	client.SetHTTPHeader(h)
	sendRequest(client)
	c.Assert(header, gc.HasLen, defaultHeaderLen+len(h))
	c.Assert(header.Get("k1"), gc.Equals, "v1")
	c.Assert(header[http.CanonicalHeaderKey("k2")], jc.DeepEquals, []string{"v2", "v3"})

	// Make a third request without custom headers.
	client.SetHTTPHeader(nil)
	sendRequest(client)
	c.Assert(header, gc.HasLen, defaultHeaderLen)
}

var getTests = []struct {
	about           string
	path            string
	nilResult       bool
	expectResult    interface{}
	expectError     string
	expectErrorCode params.ErrorCode
}{{
	about: "success",
	path:  "/wordpress/expand-id",
	expectResult: []params.ExpandedId{{
		Id: "cs:utopic/wordpress-42",
	}},
}, {
	about:     "success with nil result",
	path:      "/wordpress/expand-id",
	nilResult: true,
}, {
	about:       "non-absolute path",
	path:        "wordpress",
	expectError: `path "wordpress" is not absolute`,
}, {
	about:       "URL parse error",
	path:        "/wordpress/%zz",
	expectError: `parse .*: invalid URL escape "%zz"`,
}, {
	about:           "result with error code",
	path:            "/blahblah",
	expectError:     "not found",
	expectErrorCode: params.ErrNotFound,
}}

func (s *suite) TestGet(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	url := charm.MustParseURL("~charmers/utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, ch, 42)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, url)

	for i, test := range getTests {
		c.Logf("test %d: %s", i, test.about)

		// Send the request.
		var result json.RawMessage
		var resultPtr interface{}
		if !test.nilResult {
			resultPtr = &result
		}
		err = s.client.Get(test.path, resultPtr)

		// Check the response.
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError, gc.Commentf("error is %T; %#v", err, err))
			c.Assert(result, gc.IsNil)
			cause := errgo.Cause(err)
			if code, ok := cause.(params.ErrorCode); ok {
				c.Assert(code, gc.Equals, test.expectErrorCode)
			} else {
				c.Assert(test.expectErrorCode, gc.Equals, params.ErrorCode(""))
			}
			continue
		}
		c.Assert(err, gc.IsNil)
		if test.expectResult != nil {
			c.Assert(string(result), jc.JSONEquals, test.expectResult)
		}
	}
}

var putErrorTests = []struct {
	about           string
	path            string
	val             interface{}
	expectError     string
	expectErrorCode params.ErrorCode
}{{
	about:       "bad JSON val",
	path:        "/~charmers/utopic/wordpress-42/meta/extra-info/foo",
	val:         make(chan int),
	expectError: `cannot marshal PUT body: json: unsupported type: chan int`,
}, {
	about:       "non-absolute path",
	path:        "wordpress",
	expectError: `path "wordpress" is not absolute`,
}, {
	about:       "URL parse error",
	path:        "/wordpress/%zz",
	expectError: `parse .*: invalid URL escape "%zz"`,
}, {
	about:           "result with error code",
	path:            "/blahblah",
	expectError:     "not found",
	expectErrorCode: params.ErrNotFound,
}}

func (s *suite) TestPutError(c *gc.C) {
	url := charm.MustParseURL("~charmers/utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, charmRepo.CharmDir("wordpress"), 42)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, url)

	checkErr := func(err error, expectError string, expectErrorCode params.ErrorCode) {
		c.Assert(err, gc.ErrorMatches, expectError)
		cause := errgo.Cause(err)
		if code, ok := cause.(params.ErrorCode); ok {
			c.Assert(code, gc.Equals, expectErrorCode)
		} else {
			c.Assert(expectErrorCode, gc.Equals, params.ErrorCode(""))
		}
	}
	var result string

	for i, test := range putErrorTests {
		c.Logf("test %d: %s", i, test.about)
		err := s.client.Put(test.path, test.val)
		checkErr(err, test.expectError, test.expectErrorCode)
		err = s.client.PutWithResponse(test.path, test.val, &result)
		checkErr(err, test.expectError, test.expectErrorCode)
		c.Assert(result, gc.Equals, "")
	}
}

func (s *suite) TestPutSuccess(c *gc.C) {
	url := charm.MustParseURL("~charmers/utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, charmRepo.CharmDir("wordpress"), 42)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, url)

	perms := []string{"bob"}
	err = s.client.Put("/~charmers/utopic/wordpress-42/meta/perm/read", perms)
	c.Assert(err, gc.IsNil)
	var got []string
	err = s.client.Get("/~charmers/utopic/wordpress-42/meta/perm/read", &got)
	c.Assert(err, gc.IsNil)
	c.Assert(got, jc.DeepEquals, perms)
}

func (s *suite) TestPutWithResponseSuccess(c *gc.C) {
	// There are currently no endpoints that return a response
	// on PUT, so we'll create a fake server just to test
	// the PutWithResponse method.
	handler := func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.Copy(w, req.Body)
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()
	client := csclient.New(csclient.Params{
		URL: srv.URL,
	})

	sendBody := "hello"

	var result string
	err := client.PutWithResponse("/somewhere", sendBody, &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result, gc.Equals, sendBody)

	// Check that the method accepts a nil result.
	err = client.PutWithResponse("/somewhere", sendBody, nil)
	c.Assert(err, gc.IsNil)
}

func (s *suite) TestGetArchive(c *gc.C) {
	if jujutesting.MgoServer.WithoutV8 {
		c.Skip("mongo javascript not enabled")
	}
	key := s.checkGetArchive(c)

	// Check that the downloads count for the entity has been updated.
	s.checkCharmDownloads(c, key, 1)
}

func (s *suite) TestGetArchiveWithStatsDisabled(c *gc.C) {
	s.client.DisableStats()
	key := s.checkGetArchive(c)

	// Check that the downloads count for the entity has not been updated.
	s.checkCharmDownloads(c, key, 0)
}

func (s *suite) TestStatsUpdate(c *gc.C) {
	if jujutesting.MgoServer.WithoutV8 {
		c.Skip("mongo javascript not enabled")
	}
	key := s.checkGetArchive(c)
	s.checkCharmDownloads(c, key, 1)
	err := s.client.StatsUpdate(params.StatsUpdateRequest{
		Entries: []params.StatsUpdateEntry{{
			CharmReference: charm.MustParseURL("~charmers/utopic/wordpress-42"),
			Timestamp:      time.Now(),
			Type:           params.UpdateDeploy,
		}},
	})
	c.Assert(err, gc.IsNil)
	s.checkCharmDownloads(c, key, 2)
}

var checkDownloadsAttempt = utils.AttemptStrategy{
	Total: 1 * time.Second,
	Delay: 100 * time.Millisecond,
}

func (s *suite) checkCharmDownloads(c *gc.C, key string, expect int64) {
	stableCount := 0
	for a := checkDownloadsAttempt.Start(); a.Next(); {
		count := s.statsForKey(c, key)
		if count == expect {
			// Wait for a couple of iterations to make sure that it's stable.
			if stableCount++; stableCount >= 2 {
				return
			}
		} else {
			stableCount = 0
		}
		if !a.HasNext() {
			c.Errorf("unexpected download count for %s, got %d, want %d", key, count, expect)
		}
	}
}

func (s *suite) statsForKey(c *gc.C, key string) int64 {
	var result []params.Statistic
	err := s.client.Get("/stats/counter/"+key, &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result, gc.HasLen, 1)
	return result[0].Count
}

func (s *suite) checkGetArchive(c *gc.C) string {
	ch := charmRepo.CharmArchive(c.MkDir(), "wordpress")

	// Open the archive and calculate its hash and size.
	r, expectHash, expectSize := archiveHashAndSize(c, ch.Path)
	r.Close()

	url := charm.MustParseURL("~charmers/utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, ch, 42)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, url)

	rb, id, hash, size, err := s.client.GetArchive(url)
	c.Assert(err, gc.IsNil)
	defer rb.Close()
	c.Assert(id, jc.DeepEquals, url)
	c.Assert(hash, gc.Equals, expectHash)
	c.Assert(size, gc.Equals, expectSize)

	h := sha512.New384()
	size, err = io.Copy(h, rb)
	c.Assert(err, gc.IsNil)
	c.Assert(size, gc.Equals, expectSize)
	c.Assert(fmt.Sprintf("%x", h.Sum(nil)), gc.Equals, expectHash)

	// Return the stats key for the archive download.
	keys := []string{params.StatsArchiveDownload, "utopic", "wordpress", "charmers", "42"}
	return strings.Join(keys, ":")
}

func (s *suite) TestGetArchiveErrorNotFound(c *gc.C) {
	url := charm.MustParseURL("no-such")
	r, id, hash, size, err := s.client.GetArchive(url)
	c.Assert(err, gc.ErrorMatches, `cannot get archive: no matching charm or bundle for cs:no-such`)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
	c.Assert(r, gc.IsNil)
	c.Assert(id, gc.IsNil)
	c.Assert(hash, gc.Equals, "")
	c.Assert(size, gc.Equals, int64(0))
}

func (s *suite) TestGetArchiveTermAgreementRequired(c *gc.C) {
	ch := charmRepo.CharmArchive(c.MkDir(), "terms1")

	url := charm.MustParseURL("~charmers/utopic/terms1-1")
	err := s.client.UploadCharmWithRevision(url, ch, 1)
	c.Assert(err, jc.ErrorIsNil)
	s.setPublic(c, url)

	client := csclient.New(csclient.Params{
		URL: s.srv.URL,
	})
	s.identitySrv.SetDefaultUser("alice")

	_, _, _, _, err = client.GetArchive(url)
	c.Assert(err, gc.ErrorMatches, `cannot get archive because some terms have not been agreed to. Try "juju agree term1/1 term3/1"`)

	// user agrees to the following terms.
	s.termsChecker.agreedTerms = map[string]bool{
		"term1/1": true,
		"term2/1": true,
		"term3/1": true,
	}

	// try to get the archive again.
	_, _, _, _, err = client.GetArchive(url)
	c.Assert(err, jc.ErrorIsNil)
}

var getArchiveWithBadResponseTests = []struct {
	about       string
	response    *http.Response
	error       error
	expectError string
}{{
	about:       "http client Get failure",
	error:       errgo.New("round trip failure"),
	expectError: "cannot get archive: Get .*: round trip failure",
}, {
	about: "no entity id header",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			params.ContentHashHeader: {fakeHash},
		},
		Body:          ioutil.NopCloser(strings.NewReader("")),
		ContentLength: fakeSize,
	},
	expectError: "no " + params.EntityIdHeader + " header found in response",
}, {
	about: "invalid entity id header",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			params.ContentHashHeader: {fakeHash},
			params.EntityIdHeader:    {"no:such"},
		},
		Body:          ioutil.NopCloser(strings.NewReader("")),
		ContentLength: fakeSize,
	},
	expectError: `invalid entity id found in response: cannot parse URL "no:such": schema "no" not valid`,
}, {
	about: "partial entity id header",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			params.ContentHashHeader: {fakeHash},
			params.EntityIdHeader:    {"django"},
		},
		Body:          ioutil.NopCloser(strings.NewReader("")),
		ContentLength: fakeSize,
	},
	expectError: `archive get returned not fully qualified entity id "cs:django"`,
}, {
	about: "no hash header",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			params.EntityIdHeader: {"cs:utopic/django-42"},
		},
		Body:          ioutil.NopCloser(strings.NewReader("")),
		ContentLength: fakeSize,
	},
	expectError: "no " + params.ContentHashHeader + " header found in response",
}, {
	about: "no content length",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			params.ContentHashHeader: {fakeHash},
			params.EntityIdHeader:    {"cs:utopic/django-42"},
		},
		Body:          ioutil.NopCloser(strings.NewReader("")),
		ContentLength: -1,
	},
	expectError: "no content length found in response",
}}

func (s *suite) TestGetArchiveWithBadResponse(c *gc.C) {
	id := charm.MustParseURL("wordpress")
	for i, test := range getArchiveWithBadResponseTests {
		c.Logf("test %d: %s", i, test.about)
		cl := badResponseClient(test.response, test.error)
		_, _, _, _, err := cl.GetArchive(id)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

func (s *suite) TestUploadArchiveWithCharm(c *gc.C) {
	path := charmRepo.CharmArchivePath(c.MkDir(), "wordpress")

	// Post the archive.
	s.checkUploadArchive(c, path, "~charmers/utopic/wordpress", "cs:~charmers/utopic/wordpress-0")

	// Posting the same archive a second time does not change its resulting id.
	s.checkUploadArchive(c, path, "~charmers/utopic/wordpress", "cs:~charmers/utopic/wordpress-0")

	// Posting a different archive to the same URL increases the resulting id
	// revision.
	path = charmRepo.CharmArchivePath(c.MkDir(), "mysql")
	s.checkUploadArchive(c, path, "~charmers/utopic/wordpress", "cs:~charmers/utopic/wordpress-1")
}

func (s *suite) TestUploadArchiveWithChannels(c *gc.C) {
	path := charmRepo.CharmArchivePath(c.MkDir(), "wordpress")

	body, hash, size := archiveHashAndSize(c, path)
	defer body.Close()

	url := charm.MustParseURL("cs:~charmers/utopic/wordpress-99")
	id, err := s.client.UploadArchive(url, body, hash, size, -1, []params.Channel{
		params.EdgeChannel,
		params.CandidateChannel,
	})
	c.Assert(err, gc.IsNil)
	var meta struct {
		Published params.PublishedResponse
	}
	_, err = s.client.Meta(id, &meta)
	c.Assert(err, gc.IsNil)
	c.Assert(meta.Published.Info, jc.DeepEquals, []params.PublishedInfo{{
		Channel: params.CandidateChannel,
	}, {
		Channel: params.EdgeChannel,
	}})
}

func (s *suite) prepareBundleCharms(c *gc.C) {
	// Add the charms required by the wordpress-simple bundle to the store.
	err := s.client.UploadCharmWithRevision(
		charm.MustParseURL("~charmers/utopic/wordpress-42"),
		charmRepo.CharmArchive(c.MkDir(), "wordpress"),
		42,
	)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, charm.MustParseURL("~charmers/utopic/wordpress-42"))
	err = s.client.UploadCharmWithRevision(
		charm.MustParseURL("~charmers/utopic/mysql-47"),
		charmRepo.CharmArchive(c.MkDir(), "mysql"),
		47,
	)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, charm.MustParseURL("~charmers/utopic/mysql-47"))
}

func (s *suite) TestUploadArchiveWithBundle(c *gc.C) {
	s.prepareBundleCharms(c)
	path := charmRepo.BundleArchivePath(c.MkDir(), "wordpress-simple")
	// Post the archive.
	s.checkUploadArchive(c, path, "~charmers/bundle/wordpress-simple", "cs:~charmers/bundle/wordpress-simple-0")
}

var uploadArchiveWithBadResponseTests = []struct {
	about       string
	response    *http.Response
	error       error
	expectError string
}{{
	about:       "http client Post failure",
	error:       errgo.New("round trip failure"),
	expectError: "cannot post archive: Post .*: round trip failure",
}, {
	about: "invalid JSON in body",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Body:       ioutil.NopCloser(strings.NewReader("no id here")),
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		ContentLength: 0,
	},
	expectError: `cannot unmarshal response: .*`,
}}

func (s *suite) TestUploadArchiveWithBadResponse(c *gc.C) {
	id := charm.MustParseURL("trusty/wordpress")
	for i, test := range uploadArchiveWithBadResponseTests {
		c.Logf("test %d: %s", i, test.about)
		cl := badResponseClient(test.response, test.error)
		id, err := cl.UploadArchive(id, strings.NewReader(fakeContent), fakeHash, fakeSize, -1, nil)
		c.Assert(id, gc.IsNil)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

func (s *suite) TestUploadMultiSeriesArchive(c *gc.C) {
	path := charmRepo.CharmArchivePath(c.MkDir(), "multi-series")
	s.checkUploadArchive(c, path, "~charmers/wordpress", "cs:~charmers/wordpress-0")
}

func (s *suite) TestUploadArchiveWithServerError(c *gc.C) {
	path := charmRepo.CharmArchivePath(c.MkDir(), "wordpress")
	body, hash, size := archiveHashAndSize(c, path)
	defer body.Close()

	// Send an invalid hash so that the server returns an error.
	url := charm.MustParseURL("~charmers/trusty/wordpress")
	id, err := s.client.UploadArchive(url, body, strings.Repeat("0", len(hash)), size, -1, nil)
	c.Assert(id, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, "cannot post archive: cannot put archive blob: hash mismatch")
}

func (s *suite) checkUploadArchive(c *gc.C, path, url, expectId string) {
	// Open the archive and calculate its hash and size.
	body, hash, size := archiveHashAndSize(c, path)
	defer body.Close()

	// Post the archive.
	id, err := s.client.UploadArchive(charm.MustParseURL(url), body, hash, size, -1, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(id.String(), gc.Equals, expectId)

	// Ensure the entity has been properly added to the db.
	r, resultingId, resultingHash, resultingSize, err := s.client.GetArchive(id)
	c.Assert(err, gc.IsNil)
	defer r.Close()
	c.Assert(resultingId, gc.DeepEquals, id)
	c.Assert(resultingHash, gc.Equals, hash)
	c.Assert(resultingSize, gc.Equals, size)
}

func archiveHashAndSize(c *gc.C, path string) (r csclient.ReadSeekCloser, hash string, size int64) {
	f, err := os.Open(path)
	c.Assert(err, gc.IsNil)
	h := sha512.New384()
	size, err = io.Copy(h, f)
	c.Assert(err, gc.IsNil)
	_, err = f.Seek(0, 0)
	c.Assert(err, gc.IsNil)
	return f, fmt.Sprintf("%x", h.Sum(nil)), size
}

func (s *suite) TestUploadCharmDir(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	id, err := s.client.UploadCharm(charm.MustParseURL("~charmers/utopic/wordpress"), ch)
	c.Assert(err, gc.IsNil)
	c.Assert(id.String(), gc.Equals, "cs:~charmers/utopic/wordpress-0")
	s.checkUploadCharm(c, id, ch)
}

func (s *suite) TestUploadCharmArchive(c *gc.C) {
	ch := charmRepo.CharmArchive(c.MkDir(), "wordpress")
	id, err := s.client.UploadCharm(charm.MustParseURL("~charmers/trusty/wordpress"), ch)
	c.Assert(err, gc.IsNil)
	c.Assert(id.String(), gc.Equals, "cs:~charmers/trusty/wordpress-0")
	s.checkUploadCharm(c, id, ch)
}

func (s *suite) TestUploadCharmArchiveWithRevision(c *gc.C) {
	id := charm.MustParseURL("~charmers/trusty/wordpress-42")
	err := s.client.UploadCharmWithRevision(
		id,
		charmRepo.CharmDir("wordpress"),
		10,
	)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, id)
	ch := charmRepo.CharmArchive(c.MkDir(), "wordpress")
	s.checkUploadCharm(c, id, ch)
	id.User = ""
	id.Revision = 10
	s.checkUploadCharm(c, id, ch)
}

func (s *suite) TestUploadCharmArchiveWithUnwantedRevision(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	_, err := s.client.UploadCharm(charm.MustParseURL("~charmers/bundle/wp-20"), ch)
	c.Assert(err, gc.ErrorMatches, `revision specified in "cs:~charmers/bundle/wp-20", but should not be specified`)
}

func (s *suite) TestUploadCharmErrorUnknownType(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	unknown := struct {
		charm.Charm
	}{ch}
	id, err := s.client.UploadCharm(charm.MustParseURL("~charmers/trusty/wordpress"), unknown)
	c.Assert(err, gc.ErrorMatches, `cannot open charm archive: cannot get the archive for entity type .*`)
	c.Assert(id, gc.IsNil)
}

func (s *suite) TestUploadCharmErrorOpenArchive(c *gc.C) {
	// Since the internal code path is shared between charms and bundles, just
	// using a charm for this test also exercises the same failure for bundles.
	ch := charmRepo.CharmArchive(c.MkDir(), "wordpress")
	ch.Path = "no-such-file"
	id, err := s.client.UploadCharm(charm.MustParseURL("trusty/wordpress"), ch)
	c.Assert(err, gc.ErrorMatches, `cannot open charm archive: open no-such-file: no such file or directory`)
	c.Assert(id, gc.IsNil)
}

func (s *suite) TestUploadCharmErrorArchiveTo(c *gc.C) {
	// Since the internal code path is shared between charms and bundles, just
	// using a charm for this test also exercises the same failure for bundles.
	id, err := s.client.UploadCharm(charm.MustParseURL("trusty/wordpress"), failingArchiverTo{})
	c.Assert(err, gc.ErrorMatches, `cannot open charm archive: cannot create entity archive: bad wolf`)
	c.Assert(id, gc.IsNil)
}

type failingArchiverTo struct {
	charm.Charm
}

func (failingArchiverTo) ArchiveTo(io.Writer) error {
	return errgo.New("bad wolf")
}

func (s *suite) checkUploadCharm(c *gc.C, id *charm.URL, ch charm.Charm) {
	r, _, _, _, err := s.client.GetArchive(id)
	c.Assert(err, gc.IsNil)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	result, err := charm.ReadCharmArchiveBytes(data)
	c.Assert(err, gc.IsNil)
	// Comparing the charm metadata is sufficient for ensuring the result is
	// the same charm previously uploaded.
	c.Assert(result.Meta(), jc.DeepEquals, ch.Meta())
}

func (s *suite) TestUploadBundleDir(c *gc.C) {
	s.prepareBundleCharms(c)
	b := charmRepo.BundleDir("wordpress-simple")
	id, err := s.client.UploadBundle(charm.MustParseURL("~charmers/bundle/wordpress-simple"), b)
	c.Assert(err, gc.IsNil)
	c.Assert(id.String(), gc.Equals, "cs:~charmers/bundle/wordpress-simple-0")
	s.checkUploadBundle(c, id, b)
}

func (s *suite) TestUploadBundleArchive(c *gc.C) {
	s.prepareBundleCharms(c)
	path := charmRepo.BundleArchivePath(c.MkDir(), "wordpress-simple")
	b, err := charm.ReadBundleArchive(path)
	c.Assert(err, gc.IsNil)
	id, err := s.client.UploadBundle(charm.MustParseURL("~charmers/bundle/wp"), b)
	c.Assert(err, gc.IsNil)
	c.Assert(id.String(), gc.Equals, "cs:~charmers/bundle/wp-0")
	s.checkUploadBundle(c, id, b)
}

func (s *suite) TestUploadBundleArchiveWithUnwantedRevision(c *gc.C) {
	s.prepareBundleCharms(c)
	path := charmRepo.BundleArchivePath(c.MkDir(), "wordpress-simple")
	b, err := charm.ReadBundleArchive(path)
	c.Assert(err, gc.IsNil)
	_, err = s.client.UploadBundle(charm.MustParseURL("~charmers/bundle/wp-20"), b)
	c.Assert(err, gc.ErrorMatches, `revision specified in "cs:~charmers/bundle/wp-20", but should not be specified`)
}

func (s *suite) TestUploadBundleArchiveWithRevision(c *gc.C) {
	s.prepareBundleCharms(c)
	path := charmRepo.BundleArchivePath(c.MkDir(), "wordpress-simple")
	b, err := charm.ReadBundleArchive(path)
	c.Assert(err, gc.IsNil)
	id := charm.MustParseURL("~charmers/bundle/wp-22")
	err = s.client.UploadBundleWithRevision(id, b, 34)
	c.Assert(err, gc.IsNil)
	s.checkUploadBundle(c, id, b)
	id.User = ""
	id.Revision = 34
	s.checkUploadBundle(c, id, b)
}

func (s *suite) TestUploadBundleErrorUploading(c *gc.C) {
	// Uploading without specifying the series should return an error.
	// Note that the possible upload errors are already extensively exercised
	// as part of the client.uploadArchive tests.
	id, err := s.client.UploadBundle(
		charm.MustParseURL("~charmers/wordpress-simple"),
		charmRepo.BundleDir("wordpress-simple"),
	)
	c.Assert(err, gc.ErrorMatches, `cannot post archive: cannot read charm archive: archive file "metadata.yaml" not found`)
	c.Assert(id, gc.IsNil)
}

func (s *suite) TestUploadBundleErrorUnknownType(c *gc.C) {
	b := charmRepo.BundleDir("wordpress-simple")
	unknown := struct {
		charm.Bundle
	}{b}
	id, err := s.client.UploadBundle(charm.MustParseURL("bundle/wordpress"), unknown)
	c.Assert(err, gc.ErrorMatches, `cannot open bundle archive: cannot get the archive for entity type .*`)
	c.Assert(id, gc.IsNil)
}

func (s *suite) checkUploadBundle(c *gc.C, id *charm.URL, b charm.Bundle) {
	r, _, _, _, err := s.client.GetArchive(id)
	c.Assert(err, gc.IsNil)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	result, err := charm.ReadBundleArchiveBytes(data)
	c.Assert(err, gc.IsNil)
	// Comparing the bundle data is sufficient for ensuring the result is
	// the same bundle previously uploaded.
	c.Assert(result.Data(), jc.DeepEquals, b.Data())
}

func (s *suite) TestDoAuthorization(c *gc.C) {
	// Add a charm to be deleted.
	err := s.client.UploadCharmWithRevision(
		charm.MustParseURL("~charmers/utopic/wordpress-42"),
		charmRepo.CharmArchive(c.MkDir(), "wordpress"),
		42,
	)
	c.Assert(err, gc.IsNil)

	// Check that when we use incorrect authorization,
	// we get an error trying to set the charm's extra-info.
	client := csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     s.serverParams.AuthUsername,
		Password: "bad password",
	})
	req, err := http.NewRequest("PUT", "", nil)
	c.Assert(err, gc.IsNil)
	_, err = client.Do(req, "/~charmers/utopic/wordpress-42/meta/extra-info/foo")
	c.Assert(err, gc.ErrorMatches, "invalid user name or password")
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrUnauthorized)

	client = csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     s.serverParams.AuthUsername,
		Password: s.serverParams.AuthPassword,
	})

	// Check that the charm is still there.
	err = client.Get("/~charmers/utopic/wordpress-42/expand-id", nil)
	c.Assert(err, gc.IsNil)

	// Then check that when we use the correct authorization,
	// the delete succeeds.
	req, err = http.NewRequest("PUT", "", strings.NewReader(`"hello"`))
	c.Assert(err, gc.IsNil)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req, "/~charmers/utopic/wordpress-42/meta/extra-info/foo")
	c.Assert(err, gc.IsNil)
	resp.Body.Close()

	// Check that it's really changed.
	var val string
	err = client.Get("/utopic/wordpress-42/meta/extra-info/foo", &val)
	c.Assert(err, gc.IsNil)
	c.Assert(val, gc.Equals, "hello")
}

var getWithBadResponseTests = []struct {
	about       string
	error       error
	response    *http.Response
	responseErr error
	expectError string
}{{
	about:       "http client Get failure",
	error:       errgo.New("round trip failure"),
	expectError: "Get .*: round trip failure",
}, {
	about: "body read error",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body:          ioutil.NopCloser(&errorReader{"body read error"}),
		ContentLength: -1,
	},
	expectError: "cannot unmarshal response: error reading response body: body read error",
}, {
	about: "badly formatted json response",
	response: &http.Response{
		Status:     "200 OK",
		StatusCode: 200,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body:          ioutil.NopCloser(strings.NewReader("bad")),
		ContentLength: -1,
	},
	expectError: `cannot unmarshal response: .*`,
}, {
	about: "badly formatted json error",
	response: &http.Response{
		Status:     "404 Not found",
		StatusCode: 404,
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Proto:         "HTTP/1.0",
		ProtoMajor:    1,
		ProtoMinor:    0,
		Body:          ioutil.NopCloser(strings.NewReader("bad")),
		ContentLength: -1,
	},
	expectError: `cannot unmarshal error response "bad": .*`,
}, {
	about: "error response with empty message",
	response: &http.Response{
		Status:     "404 Not found",
		StatusCode: 404,
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Body: ioutil.NopCloser(bytes.NewReader(mustMarshalJSON(&params.Error{
			Code: "foo",
		}))),
		ContentLength: -1,
	},
	expectError: "error response with empty message .*",
}}

func (s *suite) TestGetWithBadResponse(c *gc.C) {
	for i, test := range getWithBadResponseTests {
		c.Logf("test %d: %s", i, test.about)
		cl := badResponseClient(test.response, test.error)
		var result interface{}
		err := cl.Get("/foo", &result)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

func (s *suite) TestResourceMeta(c *gc.C) {
	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Resources: map[string]resource.Meta{
			"r1": {
				Name:        "r1",
				Path:        "foo.zip",
				Description: "r1 description",
			},
		},
	})
	url := charm.MustParseURL("cs:~who/trusty/mysql")
	url, err := s.client.UploadCharm(url, ch)
	c.Assert(err, gc.IsNil)

	r1content0 := "r1 content 0"
	rev, err := s.client.UploadResource(url, "r1", "data.zip", strings.NewReader(r1content0), int64(len(r1content0)), nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(rev, gc.Equals, 0)

	r1content1 := "r1 content 1"
	rev, err = s.client.UploadResource(url, "r1", "data.zip", strings.NewReader(r1content1), int64(len(r1content1)), nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(rev, gc.Equals, 1)

	// Try with a specified revision number.
	r, err := s.client.ResourceMeta(url, "r1", 0)
	c.Assert(err, gc.IsNil)
	c.Assert(r, jc.DeepEquals, params.Resource{
		Name:        "r1",
		Type:        "file",
		Path:        "foo.zip",
		Description: "r1 description",
		Revision:    0,
		Fingerprint: resourceHash(r1content0),
		Size:        int64(len(r1content0)),
	})

	// Try with a negative (latest revision) revision.
	r, err = s.client.ResourceMeta(url, "r1", -1)
	c.Assert(err, gc.IsNil)
	c.Assert(r, jc.DeepEquals, params.Resource{
		Name:        "r1",
		Type:        "file",
		Path:        "foo.zip",
		Description: "r1 description",
		Revision:    1,
		Fingerprint: resourceHash(r1content1),
		Size:        int64(len(r1content1)),
	})
}

func badResponseClient(resp *http.Response, err error) *csclient.Client {
	client := httpbakery.NewHTTPClient()
	client.Transport = &cannedRoundTripper{
		resp:  resp,
		error: err,
	}
	bclient := httpbakery.NewClient()
	bclient.Client = client
	return csclient.New(csclient.Params{
		URL:          "http://0.1.2.3",
		User:         "bob",
		BakeryClient: bclient,
	})
}

var hyphenateTests = []struct {
	val    string
	expect string
}{{
	val:    "Hello",
	expect: "hello",
}, {
	val:    "HelloThere",
	expect: "hello-there",
}, {
	val:    "HelloHTTP",
	expect: "hello-http",
}, {
	val:    "helloHTTP",
	expect: "hello-http",
}, {
	val:    "hellothere",
	expect: "hellothere",
}, {
	val:    "Long4Camel32WithDigits45",
	expect: "long4-camel32-with-digits45",
}, {
	// The result here is equally dubious, but Go identifiers
	// should not contain underscores.
	val:    "With_Dubious_Underscore",
	expect: "with_-dubious_-underscore",
}}

func (s *suite) TestHyphenate(c *gc.C) {
	for i, test := range hyphenateTests {
		c.Logf("test %d. %q", i, test.val)
		c.Assert(csclient.Hyphenate(test.val), gc.Equals, test.expect)
	}
}

func (s *suite) TestDo(c *gc.C) {
	// Do is tested fairly comprehensively (but indirectly)
	// in TestGet, so just a trivial smoke test here.
	url := charm.MustParseURL("~charmers/utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(
		url,
		charmRepo.CharmArchive(c.MkDir(), "wordpress"),
		42,
	)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, url)
	err = s.client.PutExtraInfo(url, map[string]interface{}{
		"foo": "bar",
	})
	c.Assert(err, gc.IsNil)

	req, _ := http.NewRequest("GET", "", nil)
	resp, err := s.client.Do(req, "/wordpress/meta/extra-info/foo")
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, `"bar"`)
}

func (s *suite) TestWithChannel(c *gc.C) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, req.URL.Query().Encode())
	}))
	client := csclient.New(csclient.Params{
		URL: srv.URL,
	})

	makeRequest := func(client *csclient.Client) string {
		req, err := http.NewRequest("GET", "", nil)
		c.Assert(err, jc.ErrorIsNil)
		resp, err := client.Do(req, "/")
		c.Assert(err, jc.ErrorIsNil)
		c.Assert(resp.StatusCode, gc.Equals, http.StatusOK)
		b, err := ioutil.ReadAll(resp.Body)
		c.Assert(err, jc.ErrorIsNil)
		return string(b)
	}

	c.Assert(makeRequest(client), gc.Equals, "")
	devClient := client.WithChannel(params.EdgeChannel)
	c.Assert(makeRequest(devClient), gc.Equals, "channel="+string(params.EdgeChannel))
	// Ensure the original client has not been mutated.
	c.Assert(makeRequest(client), gc.Equals, "")
}

var metaBadTypeTests = []struct {
	result      interface{}
	expectError string
}{{
	result:      "",
	expectError: "expected pointer, not string",
}, {
	result:      new(string),
	expectError: `expected pointer to struct, not \*string`,
}, {
	result:      new(struct{ Embed }),
	expectError: "anonymous fields not supported",
}, {
	expectError: "expected valid result pointer, not nil",
}}

func (s *suite) TestMetaBadType(c *gc.C) {
	id := charm.MustParseURL("wordpress")
	for _, test := range metaBadTypeTests {
		_, err := s.client.Meta(id, test.result)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

type Embed struct{}
type embed struct{}

func (s *suite) TestMeta(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	url := charm.MustParseURL("~charmers/utopic/wordpress-42")
	purl := charm.MustParseURL("utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, ch, 42)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, url)

	// Put some extra-info.
	err = s.client.PutExtraInfo(url, map[string]interface{}{
		"attr": "value",
	})
	c.Assert(err, gc.IsNil)

	tests := []struct {
		about           string
		id              string
		expectResult    interface{}
		expectError     string
		expectErrorCode params.ErrorCode
	}{{
		about:        "no fields",
		id:           "utopic/wordpress",
		expectResult: &struct{}{},
	}, {
		about: "single field",
		id:    "utopic/wordpress",
		expectResult: &struct {
			CharmMetadata *charm.Meta
		}{
			CharmMetadata: ch.Meta(),
		},
	}, {
		about: "three fields",
		id:    "wordpress",
		expectResult: &struct {
			CharmMetadata *charm.Meta
			CharmConfig   *charm.Config
			ExtraInfo     map[string]string
		}{
			CharmMetadata: ch.Meta(),
			CharmConfig:   ch.Config(),
			ExtraInfo:     map[string]string{"attr": "value"},
		},
	}, {
		about: "tagged field",
		id:    "wordpress",
		expectResult: &struct {
			Foo  *charm.Meta `csclient:"charm-metadata"`
			Attr string      `csclient:"extra-info/attr"`
		}{
			Foo:  ch.Meta(),
			Attr: "value",
		},
	}, {
		about:           "id not found",
		id:              "bogus",
		expectResult:    &struct{}{},
		expectError:     `cannot get "/bogus/meta/any": no matching charm or bundle for cs:bogus`,
		expectErrorCode: params.ErrNotFound,
	}, {
		about: "unmarshal into invalid type",
		id:    "wordpress",
		expectResult: new(struct {
			CharmMetadata []string
		}),
		expectError: `cannot unmarshal charm-metadata: json: cannot unmarshal object into Go value of type \[]string`,
	}, {
		about: "unmarshal into struct with unexported fields",
		id:    "wordpress",
		expectResult: &struct {
			unexported    int
			CharmMetadata *charm.Meta
			// Embedded anonymous fields don't get tagged as unexported
			// due to https://code.google.com/p/go/issues/detail?id=7247
			// TODO fix in go 1.5.
			// embed
		}{
			CharmMetadata: ch.Meta(),
		},
	}, {
		about: "metadata not appropriate for charm",
		id:    "wordpress",
		expectResult: &struct {
			CharmMetadata  *charm.Meta
			BundleMetadata *charm.BundleData
		}{
			CharmMetadata: ch.Meta(),
		},
	}}
	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		// Make a result value of the same type as the expected result,
		// but empty.
		result := reflect.New(reflect.TypeOf(test.expectResult).Elem()).Interface()
		id, err := s.client.Meta(charm.MustParseURL(test.id), result)
		if test.expectError != "" {
			c.Check(err, gc.ErrorMatches, test.expectError)
			if code, ok := errgo.Cause(err).(params.ErrorCode); ok {
				c.Assert(code, gc.Equals, test.expectErrorCode)
			} else {
				c.Assert(test.expectErrorCode, gc.Equals, params.ErrorCode(""))
			}
			c.Assert(id, gc.IsNil)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(id, jc.DeepEquals, purl)
		c.Assert(result, jc.DeepEquals, test.expectResult)
	}
}

func (s *suite) TestPutMultiError(c *gc.C) {
	c.ExpectFailure("multiple error return is broken")
	ch := charmRepo.CharmDir("wordpress")
	url := charm.MustParseURL("~charmers/utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, ch, 42)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, url)
	err = s.client.Put("/meta/extra-info/foo", map[string]int{
		"~charmers/utopic/wordpress-42": 56,
		"~charmers/utopic/xxx-42":       56,
	})
	cause0 := errgo.Cause(err)
	c.Assert(cause0, gc.FitsTypeOf, (*params.Error)(nil))
	cause := cause0.(*params.Error)
	// Instead, we get just the "multiple errors" code.
	c.Assert(cause.ErrorInfo(), jc.DeepEquals, map[string]*params.Error{
		"~charmers/utopic/xxx-42": {
			Message: `no matching charm or bundle for cs:~charmers/utopic/xxx-42`,
			Code:    params.ErrNotFound,
		},
	})
}

func (s *suite) TestPutExtraInfo(c *gc.C) {
	s.checkPutInfo(c, false)
}

func (s *suite) TestPutCommonInfo(c *gc.C) {
	s.checkPutInfo(c, true)
}

func (s *suite) checkPutInfo(c *gc.C, common bool) {
	ch := charmRepo.CharmDir("wordpress")
	url := charm.MustParseURL("~charmers/utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, ch, 42)
	c.Assert(err, gc.IsNil)
	s.setPublic(c, url)

	// Put some info in.
	info := map[string]interface{}{
		"attr1": "value1",
		"attr2": []interface{}{"one", "two"},
	}
	if common {
		err = s.client.PutCommonInfo(url, info)
		c.Assert(err, gc.IsNil)
	} else {
		err = s.client.PutExtraInfo(url, info)
		c.Assert(err, gc.IsNil)
	}

	// Verify that we get it back OK.
	var valExtraInfo struct {
		ExtraInfo map[string]interface{}
	}
	var valCommonInfo struct {
		CommonInfo map[string]interface{}
	}
	if common {
		_, err = s.client.Meta(url, &valCommonInfo)
		c.Assert(err, gc.IsNil)
		c.Assert(valCommonInfo.CommonInfo, jc.DeepEquals, info)
	} else {
		_, err = s.client.Meta(url, &valExtraInfo)
		c.Assert(err, gc.IsNil)
		c.Assert(valExtraInfo.ExtraInfo, jc.DeepEquals, info)
	}

	// Put some more in.
	if common {
		err = s.client.PutCommonInfo(url, map[string]interface{}{
			"attr3": "three",
		})
		c.Assert(err, gc.IsNil)
	} else {
		err = s.client.PutExtraInfo(url, map[string]interface{}{
			"attr3": "three",
		})
		c.Assert(err, gc.IsNil)
	}
	// Verify that we get all the previous results and the new value.
	info["attr3"] = "three"
	if common {
		_, err = s.client.Meta(url, &valCommonInfo)
		c.Assert(err, gc.IsNil)
		c.Assert(valCommonInfo.CommonInfo, jc.DeepEquals, info)
	} else {
		_, err = s.client.Meta(url, &valExtraInfo)
		c.Assert(err, gc.IsNil)
		c.Assert(valExtraInfo.ExtraInfo, jc.DeepEquals, info)
	}
}

func (s *suite) TestPutExtraInfoWithError(c *gc.C) {
	err := s.client.PutExtraInfo(charm.MustParseURL("wordpress"), map[string]interface{}{"attr": "val"})
	c.Assert(err, gc.ErrorMatches, `no matching charm or bundle for cs:wordpress`)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
}

func (s *suite) TestPutCommonInfoWithError(c *gc.C) {
	err := s.client.PutCommonInfo(charm.MustParseURL("wordpress"), map[string]interface{}{"homepage": "val"})
	c.Assert(err, gc.ErrorMatches, `no matching charm or bundle for cs:wordpress`)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
}

type errorReader struct {
	error string
}

func (e *errorReader) Read(buf []byte) (int, error) {
	return 0, errgo.New(e.error)
}

type cannedRoundTripper struct {
	resp  *http.Response
	error error
}

func (r *cannedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.resp, r.error
}

func mustMarshalJSON(x interface{}) []byte {
	data, err := json.Marshal(x)
	if err != nil {
		panic(err)
	}
	return data
}

func (s *suite) TestLog(c *gc.C) {
	logs := []struct {
		typ     params.LogType
		level   params.LogLevel
		message string
		urls    []*charm.URL
	}{{
		typ:     params.IngestionType,
		level:   params.InfoLevel,
		message: "ingestion info",
		urls:    nil,
	}, {
		typ:     params.LegacyStatisticsType,
		level:   params.ErrorLevel,
		message: "statistics error",
		urls: []*charm.URL{
			charm.MustParseURL("cs:mysql"),
			charm.MustParseURL("cs:wordpress"),
		},
	}}

	for _, log := range logs {
		err := s.client.Log(log.typ, log.level, log.message, log.urls...)
		c.Assert(err, gc.IsNil)
	}
	var result []*params.LogResponse
	err := s.client.Get("/log", &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result, gc.HasLen, len(logs))
	for i, l := range result {
		c.Assert(l.Type, gc.Equals, logs[len(logs)-(1+i)].typ)
		c.Assert(l.Level, gc.Equals, logs[len(logs)-(1+i)].level)
		var msg string
		err := json.Unmarshal([]byte(l.Data), &msg)
		c.Assert(err, gc.IsNil)
		c.Assert(msg, gc.Equals, logs[len(logs)-(1+i)].message)
		c.Assert(l.URLs, jc.DeepEquals, logs[len(logs)-(1+i)].urls)
	}
}

func (s *suite) TestMacaroonAuthorization(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	curl := charm.MustParseURL("~charmers/utopic/wordpress-42")
	purl := charm.MustParseURL("utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(curl, ch, 42)
	c.Assert(err, gc.IsNil)

	err = s.client.Put("/"+curl.Path()+"/meta/perm/read", []string{"bob"})
	c.Assert(err, gc.IsNil)

	// Create a client without basic auth credentials
	client := csclient.New(csclient.Params{
		URL: s.srv.URL,
	})

	var result struct{ IdRevision struct{ Revision int } }
	// TODO 2015-01-23: once supported, rewrite the test using POST requests.
	_, err = client.Meta(purl, &result)
	c.Assert(err, gc.ErrorMatches, `cannot get "/utopic/wordpress-42/meta/any\?include=id-revision": cannot get discharge from ".*": cannot start interactive session: no supported interaction method`)
	c.Assert(errgo.Cause(err), jc.Satisfies, httpbakery.IsInteractionError)

	s.identitySrv.SetDefaultUser("bob")
	_, err = client.Meta(curl, &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result.IdRevision.Revision, gc.Equals, curl.Revision)

	s.identitySrv.SetDefaultUser("")

	client = csclient.New(csclient.Params{
		URL: s.srv.URL,
		// Note: the default client does not support any interaction methods.
		BakeryClient: httpbakery.NewClient(),
	})

	_, err = client.Meta(purl, &result)
	c.Assert(err, gc.ErrorMatches, `cannot get "/utopic/wordpress-42/meta/any\?include=id-revision": cannot get discharge from ".*": cannot start interactive session: interaction required but not possible`)
	c.Assert(result.IdRevision.Revision, gc.Equals, curl.Revision)
	c.Assert(errgo.Cause(err), jc.Satisfies, httpbakery.IsInteractionError)
}

func (s *suite) TestLogin(c *gc.C) {
	ch := charmRepo.CharmDir("wordpress")
	url := charm.MustParseURL("~charmers/utopic/wordpress-42")
	purl := charm.MustParseURL("utopic/wordpress-42")
	err := s.client.UploadCharmWithRevision(url, ch, 42)
	c.Assert(err, gc.IsNil)

	err = s.client.Put("/"+url.Path()+"/meta/perm/read", []string{"bob"})
	c.Assert(err, gc.IsNil)
	bclient := httpbakery.NewClient()
	client := csclient.New(csclient.Params{
		URL:          s.srv.URL,
		BakeryClient: bclient,
	})

	var result struct{ IdRevision struct{ Revision int } }
	_, err = client.Meta(purl, &result)
	c.Assert(err, gc.NotNil)

	// Try logging in when the discharger fails.
	err = client.Login()
	c.Assert(err, gc.ErrorMatches, `cannot retrieve the authentication macaroon: cannot get discharge from ".*": cannot start interactive session: interaction required but not possible`)

	// Allow the discharge.
	s.identitySrv.SetDefaultUser("bob")
	err = client.Login()
	c.Assert(err, gc.IsNil)

	// Change the identity server so that we're sure the cookies are being
	// used rather than the discharge mechanism.
	s.identitySrv.SetDefaultUser("")

	// Check that the request still works.
	_, err = client.Meta(purl, &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result.IdRevision.Revision, gc.Equals, url.Revision)

	// Check that we've got one cookie.
	srvURL, err := neturl.Parse(s.srv.URL)
	c.Assert(err, gc.IsNil)
	c.Assert(bclient.Jar.Cookies(srvURL), gc.HasLen, 1)

	// Log in again.
	err = client.Login()
	c.Assert(err, gc.IsNil)

	// Check that we still only have one cookie.
	c.Assert(bclient.Jar.Cookies(srvURL), gc.HasLen, 1)
}

func (s *suite) TestWhoAmI(c *gc.C) {
	client := csclient.New(csclient.Params{
		URL: s.srv.URL,
	})
	response, err := client.WhoAmI()
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from ".*": cannot start interactive session: no supported interaction method`)
	s.identitySrv.SetDefaultUser("bob")

	response, err = client.WhoAmI()
	c.Assert(err, gc.IsNil)
	c.Assert(response.User, gc.Equals, "bob")
}

func (s *suite) TestPublish(c *gc.C) {
	id := charm.MustParseURL("cs:~who/trusty/mysql")
	ch := charmRepo.CharmArchive(c.MkDir(), "mysql")

	// Upload the charm.
	url, err := s.client.UploadCharm(id, ch)
	c.Assert(err, gc.IsNil)

	// Have to make a new repo from the client, since the embedded repo is not
	// authenticated.
	err = s.client.Publish(url, []params.Channel{params.EdgeChannel}, nil)
	c.Assert(err, jc.ErrorIsNil)

	client := s.client.WithChannel(params.EdgeChannel)
	err = client.Get("/"+url.Path()+"/meta/id", nil)
	c.Assert(err, jc.ErrorIsNil)

	client = s.client.WithChannel(params.StableChannel)
	err = client.Get("/"+url.Path()+"/meta/id", nil)
	c.Assert(err, gc.ErrorMatches, ".*not found in stable channel")
}

func (s *suite) TestPublishNoChannel(c *gc.C) {
	id := charm.MustParseURL("cs:~who/trusty/mysql")
	err := s.client.Publish(id, nil, nil)
	c.Assert(err, jc.ErrorIsNil)
}

func (s *suite) TestUploadResource(c *gc.C) {
	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Resources: map[string]resource.Meta{
			"resname": {
				Name: "resname",
				Path: "foo.zip",
			},
		},
	})
	url, err := s.client.UploadCharm(charm.MustParseURL("cs:~who/trusty/mysql"), ch)
	c.Assert(err, gc.IsNil)

	for i := 0; i < 3; i++ {
		// Upload the resource.
		data := fmt.Sprintf("boo!%d", i)
		rev, err := s.client.UploadResource(url, "resname", "data.zip", strings.NewReader(data), int64(len(data)), nil)
		c.Assert(err, jc.ErrorIsNil)
		c.Assert(rev, gc.Equals, i)

		// Check that we can download it OK.
		assertGetResource(c, s.client, url, "resname", i, data)
	}
}

func (s *suite) TestUploadResourceWithRevision(c *gc.C) {
	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Resources: map[string]resource.Meta{
			"resname": {
				Name: "resname",
				Path: "foo.zip",
			},
		},
	})
	url, err := s.client.UploadCharm(charm.MustParseURL("cs:~who/trusty/mysql"), ch)
	c.Assert(err, gc.IsNil)

	// Upload the resource.
	data := "boo!"
	rev, err := s.client.UploadResourceWithRevision(url, "resname", 13, "data.zip", strings.NewReader(data), int64(len(data)), nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(rev, gc.Equals, 13)

	// Check that we can download it OK.
	assertGetResource(c, s.client, url, "resname", 13, data)
}

func (s *suite) TestDownloadLatestResource(c *gc.C) {
	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Resources: map[string]resource.Meta{
			"resname": {
				Name: "resname",
				Path: "foo.zip",
			},
		},
	})
	url, err := s.client.UploadCharm(charm.MustParseURL("cs:~who/trusty/mysql"), ch)
	c.Assert(err, gc.IsNil)

	for i := 0; i < 4; i++ {
		// Upload the resource.
		data := fmt.Sprintf("boo!%d", i)
		rev, err := s.client.UploadResource(url, "resname", "data.zip", strings.NewReader(data), int64(len(data)), nil)
		c.Assert(err, jc.ErrorIsNil)
		c.Assert(rev, gc.Equals, i)
	}
	err = s.client.Publish(url, []params.Channel{params.StableChannel}, map[string]int{"resname": 1})
	c.Assert(err, jc.ErrorIsNil)
	err = s.client.Publish(url, []params.Channel{params.EdgeChannel}, map[string]int{"resname": 2})
	c.Assert(err, jc.ErrorIsNil)

	assertGetResource(c, s.client.WithChannel(params.StableChannel), url, "resname", -1, "boo!1")
	assertGetResource(c, s.client.WithChannel(params.EdgeChannel), url, "resname", -1, "boo!2")
	assertGetResource(c, s.client.WithChannel(params.UnpublishedChannel), url, "resname", -1, "boo!3")
}

func (s *suite) TestUploadLargeResource(c *gc.C) {
	s.client.SetMinMultipartUploadSize(int64(10))

	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Resources: map[string]resource.Meta{
			"resname": {
				Name: "resname",
				Path: "foo",
			},
		},
	})
	url, err := s.client.UploadCharm(charm.MustParseURL("cs:~who/trusty/mysql"), ch)
	c.Assert(err, gc.IsNil)

	content := "abcdefghijklmnopqrstuvwxyz"
	// Upload the resource.
	progress := &testProgress{c: c}
	rev, err := s.client.UploadResource(url, "resname", "data", strings.NewReader(content), int64(len(content)), progress)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(rev, gc.Equals, 0)

	// Check that we can download it OK.
	getResult, err := s.client.GetResource(url, "resname", 0)
	c.Assert(err, jc.ErrorIsNil)
	defer getResult.Close()

	expectHash := fmt.Sprintf("%x", sha512.Sum384([]byte(content)))
	c.Assert(getResult.Hash, gc.Equals, expectHash)

	gotData, err := ioutil.ReadAll(getResult)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(string(gotData), gc.Equals, content)
	c.Assert(progress.Collected, jc.DeepEquals, []interface{}{
		startProgress{},
		transferredProgress{10},
		transferredProgress{20},
		transferredProgress{26},
		finalizingProgress{},
	})
}

func newFailProxy(serverURL string, failPattern string, failCount int) *failProxy {
	u, err := neturl.Parse(serverURL)
	if err != nil {
		panic(err)
	}
	return &failProxy{
		h:           httputil.NewSingleHostReverseProxy(u),
		failCount:   failCount,
		failPattern: regexp.MustCompile(failPattern),
	}
}

type failProxy struct {
	failPattern *regexp.Regexp
	h           http.Handler
	mu          sync.Mutex
	failCount   int
}

func (p *failProxy) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if p.failPattern.MatchString(req.URL.Path) {
		p.mu.Lock()
		n := p.failCount
		p.failCount--
		p.mu.Unlock()
		if n > 0 {
			http.Error(w, "fake server error", http.StatusGatewayTimeout)
			return
		}
	}
	p.h.ServeHTTP(w, req)
}

func (s *suite) TestUploadLargeResourceProgressWhenGetting504(c *gc.C) {
	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Resources: map[string]resource.Meta{
			"resname": {
				Name: "resname",
				Path: "foo",
			},
		},
	})
	srv := httptest.NewServer(newFailProxy(s.srv.URL, "/upload/.*/1", 2))
	client := csclient.New(csclient.Params{
		URL:      srv.URL,
		User:     s.serverParams.AuthUsername,
		Password: s.serverParams.AuthPassword,
	})
	client.SetMinMultipartUploadSize(int64(10))

	url, err := client.UploadCharm(charm.MustParseURL("cs:~who/trusty/mysql"), ch)
	c.Assert(err, gc.IsNil)

	content := "abcdefghijklmnopqrstuvwxyz"
	// Upload the resource.
	progress := &testProgress{c: c}
	rev, err := client.UploadResource(url, "resname", "data", strings.NewReader(content), int64(len(content)), progress)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(rev, gc.Equals, 0)

	// Check that we can download it OK.
	getResult, err := client.GetResource(url, "resname", 0)
	c.Assert(err, jc.ErrorIsNil)
	defer getResult.Close()

	expectHash := fmt.Sprintf("%x", sha512.Sum384([]byte(content)))
	c.Assert(getResult.Hash, gc.Equals, expectHash)

	gotData, err := ioutil.ReadAll(getResult)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(string(gotData), gc.Equals, content)
	c.Assert(progress.Collected, jc.DeepEquals, []interface{}{
		startProgress{},
		transferredProgress{10},
		transferredProgress{20},
		errorProgress{"unexpected response status from server: 504 Gateway Timeout"},
		transferredProgress{10},
		transferredProgress{20},
		errorProgress{"unexpected response status from server: 504 Gateway Timeout"},
		transferredProgress{10},
		transferredProgress{20},
		transferredProgress{26},
		finalizingProgress{},
	})
}

func (s *suite) TestUploadLargeResourceWithHashMismatch(c *gc.C) {
	s.client.SetMinMultipartUploadSize(int64(10))

	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Resources: map[string]resource.Meta{
			"resname": {
				Name: "resname",
				Path: "foo",
			},
		},
	})
	url, err := s.client.UploadCharm(charm.MustParseURL("cs:~who/trusty/mysql"), ch)
	c.Assert(err, gc.IsNil)

	r := &readerChangingUnderfoot{
		content: []byte("abcdefghijklmnopqrstuvwxyz"),
	}
	// Upload the resource.
	_, err = s.client.UploadResource(url, "resname", "data", r, int64(len(r.content)), nil)
	c.Assert(err, gc.ErrorMatches, `cannot upload part ".*": hash mismatch`)
}

func (s *suite) TestUploadTooLargeResource(c *gc.C) {
	_, err := s.client.UploadResource(charm.MustParseURL("cs:~who/trusty/mysql"), "resname", "data", strings.NewReader(""), 1<<60, nil)
	c.Assert(err, gc.ErrorMatches, `resource too big \(allowed \d+\.\d{3}GB\)`)
}

type errorReaderAt struct {
	reader io.ReaderAt
}

func (e errorReaderAt) ReadAt(p []byte, off int64) (n int, err error) {
	if off > 0 {
		return 0, errgo.New("stop here")
	}
	return e.reader.ReadAt(p, off)
}

func (s *suite) TestResumeNonExistentUploadResource(c *gc.C) {
	s.client.SetMinMultipartUploadSize(int64(10))
	content := "abcdefghiujklmetc"
	url := charm.MustParseURL("cs:~who/trusty/mysql")
	_, err := s.client.ResumeUploadResource("badid", url, "resname", "data", strings.NewReader(content), int64(len(content)), &testProgress{c: c})
	c.Assert(errgo.Cause(err), gc.Equals, csclient.ErrUploadNotFound)
	c.Check(err, gc.ErrorMatches, `upload not found`)
}

func (s *suite) TestResumeUploadResource(c *gc.C) {
	s.client.SetMinMultipartUploadSize(int64(10))

	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Resources: map[string]resource.Meta{
			"resname": {
				Name: "resname",
				Path: "foo",
			},
		},
	})
	url, err := s.client.UploadCharm(charm.MustParseURL("cs:~who/trusty/mysql"), ch)
	c.Assert(err, gc.IsNil)

	content := "abcdefghijklmnopqrstuvwxyz"
	// Upload the resource.
	progress := &testProgress{c: c}
	e := &errorReaderAt{
		reader: strings.NewReader(content),
	}
	rev, err := s.client.UploadResource(url, "resname", "data", e, int64(len(content)), progress)
	c.Assert(err, gc.ErrorMatches, "cannot read resource: stop here")
	rev, err = s.client.ResumeUploadResource(progress.uploadId, url, "resname", "data", strings.NewReader(content), int64(len(content)), progress)
	c.Assert(err, gc.Equals, nil)
	c.Assert(rev, gc.Equals, 0)

	// Check that we can download it OK.
	getResult, err := s.client.GetResource(url, "resname", 0)
	c.Assert(err, jc.ErrorIsNil)
	defer getResult.Close()

	expectHash := fmt.Sprintf("%x", sha512.Sum384([]byte(content)))
	c.Assert(getResult.Hash, gc.Equals, expectHash)

	gotData, err := ioutil.ReadAll(getResult)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(string(gotData), gc.Equals, content)
	c.Assert(progress.Collected, jc.DeepEquals, []interface{}{
		startProgress{},
		transferredProgress{10},
		startProgress{},
		transferredProgress{10},
		transferredProgress{20},
		transferredProgress{26},
		finalizingProgress{},
	})
}

type partRange struct {
	p0, p1 int64
}

var resumeUploadResourceWithDifferentPartsTests = []struct {
	about          string
	size           int64
	minPartSize    int64
	ranges         []*partRange
	expectError    string
	expectProgress []interface{}
}{{
	about:       "single gap, longer than minPartSize",
	size:        100,
	minPartSize: 10,
	ranges:      []*partRange{{0, 20}, nil, {35, 100}},
	expectProgress: []interface{}{
		startProgress{},
		transferredProgress{20},
		transferredProgress{35},
		transferredProgress{100},
		finalizingProgress{},
	},
}, {
	about:       "single gap longer than maxPartSize",
	size:        250,
	minPartSize: 10,
	ranges:      []*partRange{{0, 20}, nil, {230, 250}},
	expectError: "remaining part is too large",
}, {
	about:       "single gap smaller than minPartSize",
	size:        50,
	minPartSize: 10,
	ranges:      []*partRange{{0, 20}, nil, {25, 50}},
	expectError: "remaining part is too small",
}, {
	about:       "multiple part gap",
	size:        100,
	minPartSize: 10,
	ranges:      []*partRange{{0, 20}, nil, nil, nil, {80, 100}},
	expectProgress: []interface{}{
		startProgress{},
		transferredProgress{20},
		transferredProgress{40},
		transferredProgress{60},
		transferredProgress{80},
		transferredProgress{100},
		finalizingProgress{},
	},
}, {
	about:       "multiple part gap with unequal division",
	size:        100,
	minPartSize: 10,
	ranges:      []*partRange{{0, 20}, nil, nil, nil, {90, 100}},
	expectProgress: []interface{}{
		startProgress{},
		transferredProgress{20},
		transferredProgress{43},
		transferredProgress{66},
		transferredProgress{90},
		transferredProgress{100},
		finalizingProgress{},
	},
}, {
	about:       "gap at end",
	size:        120,
	minPartSize: 10,
	ranges:      []*partRange{{0, 20}, {20, 60}, nil, nil, nil},
	expectProgress: []interface{}{
		startProgress{},
		transferredProgress{20},
		transferredProgress{60},
		transferredProgress{70},
		transferredProgress{80},
		transferredProgress{90},
		transferredProgress{100},
		transferredProgress{110},
		transferredProgress{120},
		finalizingProgress{},
	},
}}

func (s *suite) TestResumeUploadResourceWithDifferentParts(c *gc.C) {
	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Resources: map[string]resource.Meta{
			"resname": {
				Name: "resname",
				Path: "foo",
			},
		},
	})
	url, err := s.client.UploadCharm(charm.MustParseURL("cs:~who/trusty/mysql"), ch)
	c.Assert(err, gc.IsNil)

	s.client.SetMinMultipartUploadSize(int64(10))
	expectRev := 0
	for i, test := range resumeUploadResourceWithDifferentPartsTests {
		c.Logf("test %d: %v", i, test.about)
		content := strings.Repeat(string('A'+i), int(test.size))
		s.client.SetMinMultipartUploadSize(test.minPartSize)

		uploadId := s.createPartialUpload(c, content, test.ranges)

		progress := &testProgress{c: c}
		rev, err := s.client.ResumeUploadResource(uploadId, url, "resname", "data", strings.NewReader(content), test.size, progress)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.Equals, nil)
		c.Assert(rev, gc.Equals, expectRev)
		expectRev++

		// Check that we can download it OK.
		getResult, err := s.client.GetResource(url, "resname", rev)
		c.Assert(err, jc.ErrorIsNil)

		expectHash := fmt.Sprintf("%x", sha512.Sum384([]byte(content)))
		c.Check(getResult.Hash, gc.Equals, expectHash)

		gotData, err := ioutil.ReadAll(getResult)
		c.Assert(err, jc.ErrorIsNil)
		getResult.Close()
		c.Assert(string(gotData), gc.Equals, content)
		c.Assert(progress.Collected, jc.DeepEquals, test.expectProgress)
	}
}

// createPartialUpload creates a multipart resource upload with the given content,
// putting one part for each non-nil element of parts. All fields in the Part structure
// other than Offset and Size are ignored.
//
// It returns the id of the new upload.
func (s *suite) createPartialUpload(c *gc.C, content string, ranges []*partRange) string {
	var info params.UploadInfoResponse
	// Create the upload.
	err := s.client.DoWithResponse("POST", "/upload", nil, &info)
	c.Assert(err, gc.Equals, nil)
	for i, r := range ranges {
		if r != nil {
			s.putUploadPart(c, info.UploadId, i, *r, content)
		}
	}
	return info.UploadId
}

func (s *suite) putUploadPart(c *gc.C, uploadId string, partIndex int, r partRange, content string) {
	partContent := content[r.p0:r.p1]
	hash := sha512.Sum384([]byte(partContent))
	req, err := http.NewRequest("PUT", "", strings.NewReader(partContent))
	c.Assert(err, gc.Equals, nil)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = r.p1 - r.p0
	resp, err := s.client.Do(req, fmt.Sprintf("/upload/%s/%d?hash=%x&offset=%d", uploadId, partIndex, hash, r.p0))
	c.Assert(err, gc.Equals, nil)
	resp.Body.Close()
}

func (s *suite) TestListResources(c *gc.C) {
	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Resources: map[string]resource.Meta{
			"r1": {
				Name:        "r1",
				Path:        "foo.zip",
				Description: "r1 description",
			},
			"r2": {
				Name:        "r2",
				Path:        "bar",
				Description: "r2 description",
			},
			"r3": {
				Name:        "r3",
				Path:        "missing",
				Description: "r3 description",
			},
		},
	})
	url := charm.MustParseURL("cs:~who/trusty/mysql")
	url, err := s.client.UploadCharm(url, ch)
	c.Assert(err, gc.IsNil)

	r1content := "r1 content"
	rev, err := s.client.UploadResource(url, "r1", "data.zip", strings.NewReader(r1content), int64(len(r1content)), nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(rev, gc.Equals, 0)

	r2content := "r2 content"
	rev, err = s.client.UploadResource(url, "r2", "data", strings.NewReader(r2content), int64(len(r2content)), nil)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(rev, gc.Equals, 0)

	result, err := s.client.WithChannel(params.UnpublishedChannel).ListResources(url)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(result, jc.DeepEquals, []params.Resource{{
		Name:        "r1",
		Type:        "file",
		Path:        "foo.zip",
		Description: "r1 description",
		Revision:    0,
		Fingerprint: resourceHash(r1content),
		Size:        int64(len(r1content)),
	}, {
		Name:        "r2",
		Type:        "file",
		Path:        "bar",
		Description: "r2 description",
		Revision:    0,
		Fingerprint: resourceHash(r2content),
		Size:        int64(len(r2content)),
	}, {
		Name:        "r3",
		Type:        "file",
		Path:        "missing",
		Description: "r3 description",
		Revision:    -1,
	}})
}

func resourceHash(s string) []byte {
	fp, err := resource.GenerateFingerprint(strings.NewReader(s))
	if err != nil {
		panic(err)
	}
	return fp.Bytes()
}

func (s *suite) setPublic(c *gc.C, id *charm.URL) {
	// Publish to stable.
	err := s.client.WithChannel(params.UnpublishedChannel).Put("/"+id.Path()+"/publish", &params.PublishRequest{
		Channels: []params.Channel{params.StableChannel},
	})
	c.Assert(err, jc.ErrorIsNil)

	// Allow read permissions to everyone.
	err = s.client.WithChannel(params.StableChannel).Put("/"+id.Path()+"/meta/perm/read", []string{params.Everyone})
	c.Assert(err, jc.ErrorIsNil)
}

func (s *suite) TestLatest(c *gc.C) {
	// Add some charms to the charm store.
	s.addCharm(c, "~who/trusty/mysql-0", "mysql")
	s.addCharm(c, "~who/precise/wordpress-1", "wordpress")
	s.addCharm(c, "~dalek/trusty/riak-0", "riak")
	s.addCharm(c, "~dalek/trusty/riak-1", "riak")
	s.addCharm(c, "~dalek/trusty/riak-3", "riak")
	_, url := s.addCharm(c, "~who/utopic/varnish-0", "varnish")

	// Change permissions on one of the charms so that it is not readable by
	// anyone.
	err := s.client.Put("/"+url.Path()+"/meta/perm/read", []string{"dalek"})
	c.Assert(err, jc.ErrorIsNil)

	// Define the tests to be run.
	tests := []struct {
		about string
		urls  []*charm.URL
		revs  []csclient.CharmRevision
	}{{
		about: "no urls",
	}, {
		about: "charm not found",
		urls:  []*charm.URL{charm.MustParseURL("cs:trusty/no-such-42")},
		revs: []csclient.CharmRevision{{
			Err: params.ErrNotFound,
		}},
	}, {
		about: "resolve",
		urls: []*charm.URL{
			charm.MustParseURL("cs:~who/trusty/mysql-42"),
			charm.MustParseURL("cs:~who/trusty/mysql-0"),
			charm.MustParseURL("cs:~who/trusty/mysql"),
		},
		revs: []csclient.CharmRevision{{
			Revision: 0,
		}, {
			Revision: 0,
		}, {
			Revision: 0,
		}},
	}, {
		about: "multiple charms",
		urls: []*charm.URL{
			charm.MustParseURL("cs:~who/precise/wordpress"),
			charm.MustParseURL("cs:~who/trusty/mysql-47"),
			charm.MustParseURL("cs:~dalek/trusty/no-such"),
			charm.MustParseURL("cs:~dalek/trusty/riak-0"),
		},
		revs: []csclient.CharmRevision{{
			Revision: 1,
		}, {
			Revision: 0,
		}, {
			Err: params.ErrNotFound,
		}, {
			Revision: 3,
		}},
	}, {
		about: "unauthorized",
		urls: []*charm.URL{
			charm.MustParseURL("cs:~who/precise/wordpress"),
			url,
		},
		revs: []csclient.CharmRevision{{
			Revision: 1,
		}, {
			Err: params.ErrNotFound,
		}},
	}}

	// Run the tests.
	client := csclient.New(csclient.Params{
		URL: s.srv.URL,
	})
	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		revs, err := client.Latest(test.urls)
		c.Assert(err, jc.ErrorIsNil)
		c.Check(revs, jc.DeepEquals, test.revs)
	}
}

// addCharm uploads a charm a promulgated revision to the testing charm store
func (s *suite) addCharm(c *gc.C, urlStr, name string) (charm.Charm, *charm.URL) {
	id := charm.MustParseURL(urlStr)
	promulgatedRevision := -1
	if id.User == "" {
		id.User = "who"
		promulgatedRevision = id.Revision
	}
	ch := charmRepo.CharmArchive(c.MkDir(), name)

	// Upload the charm.
	err := s.client.UploadCharmWithRevision(id, ch, promulgatedRevision)
	c.Assert(err, gc.IsNil)

	// Allow read permissions to everyone.
	s.setPublic(c, id)

	return ch, id
}

func (s *suite) TestDockerResourceUploadInfo(c *gc.C) {
	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Series: []string{"kubernetes"},
		Resources: map[string]resource.Meta{
			"r1": {
				Name:        "r1",
				Type:        resource.TypeContainerImage,
				Description: "r1 description",
			},
		},
	})
	url := charm.MustParseURL("cs:~who/ktest")
	url, err := s.client.UploadCharm(url, ch)
	c.Assert(err, gc.IsNil)

	info, err := s.client.DockerResourceUploadInfo(url, "r1")
	c.Assert(err, gc.IsNil)
	c.Assert(info.ImageName, gc.Equals, "0.1.2.3/who/ktest/r1")
	c.Assert(info.Username, gc.Equals, "docker-uploader")
	c.Assert(info.Password, gc.Not(gc.Equals), "")
}

func (s *suite) TestDockerResourceUploadInfoNotFound(c *gc.C) {
	_, err := s.client.DockerResourceUploadInfo(charm.MustParseURL("cs:~who/ktest"), "r1")
	c.Assert(err, gc.ErrorMatches, `no matching charm or bundle for cs:~who/ktest`)
}

func (s *suite) TestDockerResourceDownloadInfo(c *gc.C) {
	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Series: []string{"kubernetes"},
		Resources: map[string]resource.Meta{
			"r1": {
				Name:        "r1",
				Type:        resource.TypeContainerImage,
				Description: "r1 description",
			},
		},
	})
	url := charm.MustParseURL("cs:~who/ktest")
	url, err := s.client.UploadCharm(url, ch)
	c.Assert(err, gc.IsNil)

	rev, err := s.client.AddDockerResource(url, "r1", "", "sha256:0a69ca95710aa3fb5a9f8b60cbe2cb5f25485a6c739dd9d95e16c1e8d51d57b4319ac7d1daeaf8f7399e13f3d280c239407f6ea6016ed325c6304dd97c17c296")
	c.Assert(err, gc.IsNil)
	c.Assert(rev, gc.Equals, 0)

	info, err := s.client.DockerResourceDownloadInfo(url, "r1", 0)
	c.Assert(err, gc.IsNil)
	c.Assert(info.ImageName, gc.Equals, "0.1.2.3/who/ktest/r1@sha256:0a69ca95710aa3fb5a9f8b60cbe2cb5f25485a6c739dd9d95e16c1e8d51d57b4319ac7d1daeaf8f7399e13f3d280c239407f6ea6016ed325c6304dd97c17c296")
	c.Assert(info.Username, gc.Equals, "docker-registry")
	c.Assert(info.Password, gc.Not(gc.Equals), "")
}

func (s *suite) TestDockerResourceDownloadInfoLatest(c *gc.C) {
	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Series: []string{"kubernetes"},
		Resources: map[string]resource.Meta{
			"r1": {
				Name:        "r1",
				Type:        resource.TypeContainerImage,
				Description: "r1 description",
			},
		},
	})
	url := charm.MustParseURL("cs:~who/ktest")
	url, err := s.client.UploadCharm(url, ch)
	c.Assert(err, gc.IsNil)

	rev, err := s.client.AddDockerResource(url, "r1", "", "sha256:2d466e55f49f5b466a74473ba2d8db0d4f2b4a9a92ab312c9d391cf612b44648")
	c.Assert(err, gc.IsNil)
	c.Assert(rev, gc.Equals, 0)

	rev, err = s.client.AddDockerResource(url, "r1", "", "sha256:c61c7057e20756c1c38595fc5d0249d3e7cd5fea8fc1e6e01bcda1c6980c9e42")
	c.Assert(err, gc.IsNil)
	c.Assert(rev, gc.Equals, 1)

	err = s.client.Publish(url, []params.Channel{params.StableChannel}, map[string]int{"r1": 0})
	c.Assert(err, jc.ErrorIsNil)

	info, err := s.client.WithChannel(params.StableChannel).DockerResourceDownloadInfo(url, "r1", -1)
	c.Assert(err, gc.IsNil)
	c.Assert(info.ImageName, gc.Equals, "0.1.2.3/who/ktest/r1@sha256:2d466e55f49f5b466a74473ba2d8db0d4f2b4a9a92ab312c9d391cf612b44648")
	c.Assert(info.Username, gc.Equals, "docker-registry")
	c.Assert(info.Password, gc.Not(gc.Equals), "")

	info, err = s.client.WithChannel(params.UnpublishedChannel).DockerResourceDownloadInfo(url, "r1", -1)
	c.Assert(err, gc.IsNil)
	c.Assert(info.ImageName, gc.Equals, "0.1.2.3/who/ktest/r1@sha256:c61c7057e20756c1c38595fc5d0249d3e7cd5fea8fc1e6e01bcda1c6980c9e42")
	c.Assert(info.Username, gc.Equals, "docker-registry")
	c.Assert(info.Password, gc.Not(gc.Equals), "")

}

func (s *suite) TestAddDockerResource(c *gc.C) {
	ch := charmtesting.NewCharmMeta(&charm.Meta{
		Series: []string{"kubernetes"},
		Resources: map[string]resource.Meta{
			"r1": {
				Name:        "r1",
				Type:        resource.TypeContainerImage,
				Description: "r1 description",
			},
		},
	})
	url := charm.MustParseURL("cs:~who/ktest")
	url, err := s.client.UploadCharm(url, ch)
	c.Assert(err, gc.IsNil)

	rev, err := s.client.AddDockerResource(url, "r1", "", "sha256:0a69ca95710aa3fb5a9f8b60cbe2cb5f25485a6c739dd9d95e16c1e8d51d57b4319ac7d1daeaf8f7399e13f3d280c239407f6ea6016ed325c6304dd97c17c296")
	c.Assert(err, gc.IsNil)
	c.Assert(rev, gc.Equals, 0)
}

func (s *suite) TestAddDockerResourceNotFound(c *gc.C) {
	_, err := s.client.AddDockerResource(charm.MustParseURL("cs:~who/ktest"), "r1", "", "sha256:0a69ca95710aa3fb5a9f8b60cbe2cb5f25485a6c739dd9d95e16c1e8d51d57b4319ac7d1daeaf8f7399e13f3d280c239407f6ea6016ed325c6304dd97c17c296")
	c.Assert(err, gc.ErrorMatches, `no matching charm or bundle for cs:~who/ktest`)
}

// hashOfCharm returns the SHA256 hash sum for the given charm name.
func hashOfCharm(c *gc.C, name string) string {
	path := charmRepo.CharmArchivePath(c.MkDir(), name)
	return hashOfPath(c, path)
}

// hashOfPath returns the SHA256 hash sum for the given path.
func hashOfPath(c *gc.C, path string) string {
	f, err := os.Open(path)
	c.Assert(err, jc.ErrorIsNil)
	defer f.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	c.Assert(err, jc.ErrorIsNil)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

type dischargeAcquirerFunc func(cav macaroon.Caveat) (*macaroon.Macaroon, error)

func (f dischargeAcquirerFunc) AcquireDischarge(cav macaroon.Caveat) (*macaroon.Macaroon, error) {
	return f(cav)
}

// termsChecker mocks out the functionality of the terms service for testing
// purposes.
type termsChecker struct {
	agreedTerms map[string]bool
}

func (m *termsChecker) CheckThirdPartyCaveat(ctx context.Context, p httpbakery.ThirdPartyCaveatCheckerParams) ([]checkers.Caveat, error) {
	cond, args, err := checkers.ParseCaveat(string(p.Caveat.Condition))
	if err != nil {
		return nil, errgo.Mask(err)
	}
	terms := strings.Fields(args)

	if cond != "has-agreed" {
		return nil, errgo.Newf("caveat not recognized %v", cond)
	}

	needsAgreement := []string{}
	for _, term := range terms {
		if !m.agreedTerms[term] {
			needsAgreement = append(needsAgreement, term)
		}
	}
	if len(needsAgreement) == 0 {
		return []checkers.Caveat{}, nil
	}
	return nil, &httpbakery.Error{Code: "term agreement required", Message: fmt.Sprintf("term agreement required: %s", strings.Join(needsAgreement, " "))}
}

type testProgress struct {
	c         *gc.C
	Collected []interface{}
	uploadId  string
}

type startProgress struct {
}

type transferredProgress struct {
	n int64
}

type errorProgress struct {
	error string
}

type finalizingProgress struct {
}

func (p *testProgress) Start(uploadId string, expires time.Time) {
	p.Collected = append(p.Collected, startProgress{})
	p.c.Assert(uploadId, gc.NotNil)
	p.c.Assert(expires.After(time.Now()), gc.Equals, true, gc.Commentf("expires %v", expires))
	p.uploadId = uploadId
}

func (p *testProgress) Transferred(total int64) {
	p.Collected = append(p.Collected, transferredProgress{total})
}

func (p *testProgress) Error(err error) {
	p.Collected = append(p.Collected, errorProgress{
		error: err.Error(),
	})
}

func (p *testProgress) Finalizing() {
	p.Collected = append(p.Collected, finalizingProgress{})
}

type readerChangingUnderfoot struct {
	mu      sync.Mutex
	content []byte
}

func (r *readerChangingUnderfoot) ReadAt(buf []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := copy(buf, r.content[off:])
	// We've read the content once. Now change it to something else.
	for i := off; i < off+int64(n); i++ {
		r.content[i] = 'x'
	}
	return n, nil
}

func assertGetResource(c *gc.C, client *csclient.Client, url *charm.URL, resourceName string, rev int, expectData string) {
	getResult, err := client.GetResource(url, "resname", rev)
	c.Assert(err, jc.ErrorIsNil)
	defer getResult.Close()

	gotData, err := ioutil.ReadAll(getResult)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(string(gotData), gc.Equals, expectData)

	expectHash := fmt.Sprintf("%x", sha512.Sum384([]byte(expectData)))
	c.Assert(getResult.Hash, gc.Equals, expectHash)
}
