// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo_test

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmstore.v5-unstable"

	"gopkg.in/juju/charmrepo.v2-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	charmtesting "gopkg.in/juju/charmrepo.v2-unstable/testing"
)

type charmStoreSuite struct {
	jujutesting.IsolationSuite
}

var _ = gc.Suite(&charmStoreSuite{})

func (s *charmStoreSuite) TestDefaultURL(c *gc.C) {
	repo := charmrepo.NewCharmStore(charmrepo.NewCharmStoreParams{})
	c.Assert(repo.Client().ServerURL(), gc.Equals, csclient.ServerURL)
}

type charmStoreBaseSuite struct {
	charmtesting.IsolatedMgoSuite
	srv     *httptest.Server
	client  *csclient.Client
	handler charmstore.HTTPCloseHandler
	repo    *charmrepo.CharmStore
}

var _ = gc.Suite(&charmStoreBaseSuite{})

func (s *charmStoreBaseSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.startServer(c)
	s.repo = charmrepo.NewCharmStore(charmrepo.NewCharmStoreParams{
		URL: s.srv.URL,
	})
	s.PatchValue(&charmrepo.CacheDir, c.MkDir())
}

func (s *charmStoreBaseSuite) TearDownTest(c *gc.C) {
	s.srv.Close()
	s.handler.Close()
	s.IsolatedMgoSuite.TearDownTest(c)
}

func (s *charmStoreBaseSuite) startServer(c *gc.C) {
	serverParams := charmstore.ServerParams{
		AuthUsername: "test-user",
		AuthPassword: "test-password",
	}

	db := s.Session.DB("charmstore")
	handler, err := charmstore.NewServer(db, nil, "", serverParams, charmstore.V5)
	c.Assert(err, gc.IsNil)
	s.handler = handler
	s.srv = httptest.NewServer(handler)
	s.client = csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     serverParams.AuthUsername,
		Password: serverParams.AuthPassword,
	})
}

// addCharm uploads a charm a promulgated revision to the testing charm store,
// and returns the resulting charm and charm URL.
func (s *charmStoreBaseSuite) addCharm(c *gc.C, urlStr, name string) (charm.Charm, *charm.URL) {
	id := charm.MustParseURL(urlStr)
	promulgatedRevision := -1
	if id.User == "" {
		id.User = "who"
		promulgatedRevision = id.Revision
	}
	ch := TestCharms.CharmArchive(c.MkDir(), name)

	// Upload the charm.
	err := s.client.UploadCharmWithRevision(id, ch, promulgatedRevision)
	c.Assert(err, gc.IsNil)

	s.setPublic(c, id)
	return ch, id
}

// addCharmNoRevision uploads a charm to the testing charm store, and returns the
// resulting charm and charm URL.
func (s *charmStoreBaseSuite) addCharmNoRevision(c *gc.C, urlStr, name string) (charm.Charm, *charm.URL) {
	id := charm.MustParseURL(urlStr)
	if id.User == "" {
		id.User = "who"
	}
	ch := TestCharms.CharmArchive(c.MkDir(), name)

	// Upload the charm.
	url, err := s.client.UploadCharm(id, ch)
	c.Assert(err, gc.IsNil)

	s.setPublic(c, id)

	return ch, url
}

// addBundle uploads a bundle to the testing charm store, and returns the
// resulting bundle and bundle URL.
func (s *charmStoreBaseSuite) addBundle(c *gc.C, urlStr, name string) (charm.Bundle, *charm.URL) {
	id := charm.MustParseURL(urlStr)
	promulgatedRevision := -1
	if id.User == "" {
		id.User = "who"
		promulgatedRevision = id.Revision
	}
	b := TestCharms.BundleArchive(c.MkDir(), name)

	// Upload the bundle.
	err := s.client.UploadBundleWithRevision(id, b, promulgatedRevision)
	c.Assert(err, gc.IsNil)

	s.setPublic(c, id)

	// Return the bundle and its URL.
	return b, id
}

func (s *charmStoreBaseSuite) setPublic(c *gc.C, id *charm.URL, channels ...params.Channel) {
	if len(channels) == 0 {
		channels = []params.Channel{
			params.StableChannel,
			params.DevelopmentChannel,
			params.UnpublishedChannel,
		}
	}
	unpublished := false
	for i, channel := range channels {
		if channel == params.UnpublishedChannel {
			unpublished = true
			channels = append(channels[:i], channels[i+1:]...)
			break
		}
	}
	err := s.client.WithChannel(params.UnpublishedChannel).Put("/"+id.Path()+"/publish", &params.PublishRequest{
		Channels: channels,
	})
	c.Assert(err, jc.ErrorIsNil)

	if unpublished {
		channels = append(channels, params.UnpublishedChannel)
	}
	for _, channel := range channels {
		// Allow read permissions to everyone.
		err = s.client.WithChannel(channel).Put("/"+id.Path()+"/meta/perm/read", []string{params.Everyone})
		c.Assert(err, jc.ErrorIsNil)
	}
}

type charmStoreRepoSuite struct {
	charmStoreBaseSuite
}

var _ = gc.Suite(&charmStoreRepoSuite{})

// checkCharmDownloads checks that the charm represented by the given URL has
// been downloaded the expected number of times.
func (s *charmStoreRepoSuite) checkCharmDownloads(c *gc.C, url *charm.URL, expect int) {
	key := []string{params.StatsArchiveDownload, url.Series, url.Name, url.User, strconv.Itoa(url.Revision)}
	path := "/stats/counter/" + strings.Join(key, ":")
	var count int

	getDownloads := func() int {
		var result []params.Statistic
		err := s.client.Get(path, &result)
		c.Assert(err, jc.ErrorIsNil)
		return int(result[0].Count)
	}

	for retry := 0; retry < 10; retry++ {
		time.Sleep(100 * time.Millisecond)
		if count = getDownloads(); count == expect {
			if expect == 0 && retry < 2 {
				// Wait a bit to make sure.
				continue
			}
			return
		}
	}
	c.Errorf("downloads count for %s is %d, expected %d", url, count, expect)
}

func (s *charmStoreRepoSuite) TestNewCharmStoreFromClient(c *gc.C) {
	client := csclient.New(csclient.Params{URL: csclient.ServerURL})

	repo := charmrepo.NewCharmStoreFromClient(client)

	c.Check(repo.Client().ServerURL(), gc.Equals, csclient.ServerURL)
}

func (s *charmStoreRepoSuite) TestGet(c *gc.C) {
	expect, url := s.addCharm(c, "cs:~who/trusty/mysql-0", "mysql")
	ch, err := s.repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)
	checkCharm(c, ch, expect)
}

func (s *charmStoreRepoSuite) TestGetPromulgated(c *gc.C) {
	expect, url := s.addCharm(c, "trusty/mysql-42", "mysql")
	ch, err := s.repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)
	checkCharm(c, ch, expect)
}

func (s *charmStoreRepoSuite) TestGetRevisions(c *gc.C) {
	s.addCharm(c, "~dalek/trusty/riak-0", "riak")
	expect1, url1 := s.addCharm(c, "~dalek/trusty/riak-1", "riak")
	expect2, _ := s.addCharm(c, "~dalek/trusty/riak-2", "riak")

	// Retrieve an old revision.
	ch, err := s.repo.Get(url1)
	c.Assert(err, jc.ErrorIsNil)
	checkCharm(c, ch, expect1)

	// Retrieve the latest revision.
	ch, err = s.repo.Get(charm.MustParseURL("cs:~dalek/trusty/riak"))
	c.Assert(err, jc.ErrorIsNil)
	checkCharm(c, ch, expect2)
}

func (s *charmStoreRepoSuite) TestGetCache(c *gc.C) {
	_, url := s.addCharm(c, "~who/trusty/mysql-42", "mysql")
	ch, err := s.repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)
	path := ch.(*charm.CharmArchive).Path
	c.Assert(hashOfPath(c, path), gc.Equals, hashOfCharm(c, "mysql"))
}

func (s *charmStoreRepoSuite) TestGetSameCharm(c *gc.C) {
	_, url := s.addCharm(c, "precise/wordpress-47", "wordpress")
	getModTime := func(path string) time.Time {
		info, err := os.Stat(path)
		c.Assert(err, jc.ErrorIsNil)
		return info.ModTime()
	}

	// Retrieve a charm.
	ch1, err := s.repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)

	// Retrieve its cache file modification time.
	path := ch1.(*charm.CharmArchive).Path
	modTime := getModTime(path)

	// Retrieve the same charm again.
	ch2, err := s.repo.Get(url.WithRevision(-1))
	c.Assert(err, jc.ErrorIsNil)

	// Check this is the same charm, and its underlying cache file is the same.
	checkCharm(c, ch2, ch1)
	c.Assert(ch2.(*charm.CharmArchive).Path, gc.Equals, path)

	// Check the same file has been reused.
	c.Assert(modTime.Equal(getModTime(path)), jc.IsTrue)
}

func (s *charmStoreRepoSuite) TestGetInvalidCache(c *gc.C) {
	_, url := s.addCharm(c, "~who/trusty/mysql-1", "mysql")

	// Retrieve a charm.
	ch1, err := s.repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)

	// Modify its cache file to make it invalid.
	path := ch1.(*charm.CharmArchive).Path
	err = ioutil.WriteFile(path, []byte("invalid"), 0644)
	c.Assert(err, jc.ErrorIsNil)

	// Retrieve the same charm again.
	_, err = s.repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)

	// Check that the cache file have been properly rewritten.
	c.Assert(hashOfPath(c, path), gc.Equals, hashOfCharm(c, "mysql"))
}

func (s *charmStoreRepoSuite) TestGetIncreaseStats(c *gc.C) {
	if jujutesting.MgoServer.WithoutV8 {
		c.Skip("mongo javascript not enabled")
	}
	_, url := s.addCharm(c, "~who/precise/wordpress-2", "wordpress")

	// Retrieve the charm.
	_, err := s.repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)
	s.checkCharmDownloads(c, url, 1)

	// Retrieve the charm again.
	_, err = s.repo.Get(url)
	c.Assert(err, jc.ErrorIsNil)
	s.checkCharmDownloads(c, url, 2)
}

func (s *charmStoreRepoSuite) TestGetErrorBundle(c *gc.C) {
	ch, err := s.repo.Get(charm.MustParseURL("cs:bundle/django"))
	c.Assert(err, gc.ErrorMatches, `expected a charm URL, got bundle URL "cs:bundle/django"`)
	c.Assert(ch, gc.IsNil)
}

func (s *charmStoreRepoSuite) TestGetErrorCacheDir(c *gc.C) {
	parentDir := c.MkDir()
	err := os.Chmod(parentDir, 0)
	c.Assert(err, jc.ErrorIsNil)
	defer os.Chmod(parentDir, 0755)
	s.PatchValue(&charmrepo.CacheDir, filepath.Join(parentDir, "cache"))

	ch, err := s.repo.Get(charm.MustParseURL("cs:trusty/django"))
	c.Assert(err, gc.ErrorMatches, `cannot create the cache directory: .*: permission denied`)
	c.Assert(ch, gc.IsNil)
}

func (s *charmStoreRepoSuite) TestGetErrorCharmNotFound(c *gc.C) {
	ch, err := s.repo.Get(charm.MustParseURL("cs:trusty/no-such"))
	c.Assert(err, gc.ErrorMatches, `cannot retrieve "cs:trusty/no-such": charm not found`)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrNotFound)
	c.Assert(ch, gc.IsNil)
}

func (s *charmStoreRepoSuite) TestGetErrorServer(c *gc.C) {
	// Set up a server always returning errors.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"Message": "bad wolf", "Code": "bad request"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	// Try getting a charm from the server.
	repo := charmrepo.NewCharmStore(charmrepo.NewCharmStoreParams{
		URL: srv.URL,
	})
	ch, err := repo.Get(charm.MustParseURL("cs:trusty/django"))
	c.Assert(err, gc.ErrorMatches, `cannot retrieve charm "cs:trusty/django": cannot get archive: bad wolf`)
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrBadRequest)
	c.Assert(ch, gc.IsNil)
}

func (s *charmStoreRepoSuite) TestGetErrorHashMismatch(c *gc.C) {
	_, url := s.addCharm(c, "trusty/riak-0", "riak")

	// Set up a proxy server that modifies the returned hash.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		s.handler.ServeHTTP(rec, r)
		w.Header().Set(params.EntityIdHeader, rec.Header().Get(params.EntityIdHeader))
		w.Header().Set(params.ContentHashHeader, "invalid")
		w.Write(rec.Body.Bytes())
	}))
	defer srv.Close()

	// Try getting a charm from the server.
	repo := charmrepo.NewCharmStore(charmrepo.NewCharmStoreParams{
		URL: srv.URL,
	})
	ch, err := repo.Get(url)
	c.Assert(err, gc.ErrorMatches, `hash mismatch; network corruption\?`)
	c.Assert(ch, gc.IsNil)
}

func (s *charmStoreRepoSuite) TestGetBundle(c *gc.C) {
	// Note that getting a bundle shares most of the logic with charm
	// retrieval. For this reason, only bundle specific code is tested.
	s.addCharm(c, "cs:trusty/mysql-0", "mysql")
	s.addCharm(c, "cs:trusty/wordpress-0", "wordpress")
	expect, url := s.addBundle(c, "cs:~who/bundle/wordpress-simple-42", "wordpress-simple")
	b, err := s.repo.GetBundle(url)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(b.Data(), jc.DeepEquals, expect.Data())
	c.Assert(b.ReadMe(), gc.Equals, expect.ReadMe())
}

func (s *charmStoreRepoSuite) TestGetBundleErrorCharm(c *gc.C) {
	ch, err := s.repo.GetBundle(charm.MustParseURL("cs:trusty/django"))
	c.Assert(err, gc.ErrorMatches, `expected a bundle URL, got charm URL "cs:trusty/django"`)
	c.Assert(ch, gc.IsNil)
}

type charmStoreResolveSuite struct {
	charmStoreBaseSuite
}

var _ = gc.Suite(&charmStoreResolveSuite{})

var resolveTests = []struct {
	id              string
	url             string
	supportedSeries []string
	err             string
}{{
	id:              "~who/mysql",
	url:             "cs:~who/trusty/mysql-0",
	supportedSeries: []string{"trusty"},
}, {
	id:              "~who/trusty/mysql",
	url:             "cs:~who/trusty/mysql-0",
	supportedSeries: []string{"trusty"},
}, {
	id:              "~who/wordpress",
	url:             "cs:~who/precise/wordpress-2",
	supportedSeries: []string{"precise"},
}, {
	id:  "~who/wordpress-2",
	err: `cannot resolve URL "cs:~who/wordpress-2": charm or bundle not found`,
}, {
	id:              "~dalek/riak",
	url:             "cs:~dalek/utopic/riak-42",
	supportedSeries: []string{"utopic"},
}, {
	id:              "~dalek/utopic/riak-42",
	url:             "cs:~dalek/utopic/riak-42",
	supportedSeries: []string{"utopic"},
}, {
	id:              "utopic/mysql",
	url:             "cs:utopic/mysql-47",
	supportedSeries: []string{"utopic"},
}, {
	id:              "utopic/mysql-47",
	url:             "cs:utopic/mysql-47",
	supportedSeries: []string{"utopic"},
}, {
	id:              "~who/multi-series",
	url:             "cs:~who/multi-series-0",
	supportedSeries: []string{"trusty", "precise", "quantal"},
}, {
	id:  "~dalek/utopic/riak-100",
	err: `cannot resolve URL "cs:~dalek/utopic/riak-100": charm not found`,
}, {
	id:  "bundle/no-such",
	err: `cannot resolve URL "cs:bundle/no-such": bundle not found`,
}, {
	id:  "no-such",
	err: `cannot resolve URL "cs:no-such": charm or bundle not found`,
}}

func (s *charmStoreResolveSuite) addCharms(c *gc.C) {
	// Add promulgated entities first so that the base entity
	// is marked as promulgated when it first gets inserted.
	s.addCharm(c, "utopic/mysql-47", "mysql")
	s.addCharmNoRevision(c, "multi-series", "multi-series")

	s.addCharm(c, "~who/trusty/mysql-0", "mysql")
	s.addCharm(c, "~who/precise/wordpress-2", "wordpress")
	s.addCharm(c, "~dalek/utopic/riak-42", "riak")
}

func (s *charmStoreResolveSuite) TestResolve(c *gc.C) {
	s.addCharms(c)
	for i, test := range resolveTests {
		c.Logf("test %d: %s", i, test.id)
		ref, supportedSeries, err := s.repo.Resolve(charm.MustParseURL(test.id))
		if test.err != "" {
			c.Check(err.Error(), gc.Equals, test.err)
			c.Check(ref, gc.IsNil)
			continue
		}
		c.Assert(err, jc.ErrorIsNil)
		c.Check(ref, jc.DeepEquals, charm.MustParseURL(test.url))
		c.Check(supportedSeries, jc.SameContents, test.supportedSeries)
	}
}

// hashOfCharm returns the SHA256 hash sum for the given charm name.
func hashOfCharm(c *gc.C, name string) string {
	path := TestCharms.CharmArchivePath(c.MkDir(), name)
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

// checkCharm checks that the given charms have the same attributes.
func checkCharm(c *gc.C, ch, expect charm.Charm) {
	c.Assert(ch.Actions(), jc.DeepEquals, expect.Actions())
	c.Assert(ch.Config(), jc.DeepEquals, expect.Config())
	c.Assert(ch.Meta(), jc.DeepEquals, expect.Meta())
}
