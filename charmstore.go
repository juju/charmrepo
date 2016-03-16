// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmrepo // import "gopkg.in/juju/charmrepo.v2-unstable"

import (
	"crypto/sha512"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/juju/utils"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charm.v6-unstable/resource"
	"gopkg.in/macaroon-bakery.v1/httpbakery"

	"gopkg.in/juju/charmrepo.v2-unstable/csclient"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
)

// CacheDir stores the charm cache directory path.
var CacheDir string

// CharmStore is a repository Interface that provides access to the public Juju
// charm store.
type CharmStore struct {
	client apiClient
}

var _ Interface = (*CharmStore)(nil)

// NewCharmStoreParams holds parameters for instantiating a new CharmStore.
type NewCharmStoreParams struct {
	// URL holds the root endpoint URL of the charm store,
	// with no trailing slash, not including the version.
	// For example https://api.jujucharms.com/charmstore
	// If empty, the default charm store client location is used.
	URL string

	// BakeryClient holds the bakery client to use when making
	// requests to the store. This is used in preference to
	// HTTPClient.
	BakeryClient *httpbakery.Client

	// HTTPClient holds the HTTP client to use when making
	// requests to the store. If nil, httpbakery.NewHTTPClient will
	// be used.
	HTTPClient *http.Client

	// VisitWebPage is called when authorization requires that
	// the user visits a web page to authenticate themselves.
	// If nil, no interaction will be allowed. This field
	// is ignored if BakeryClient is provided.
	VisitWebPage func(url *url.URL) error

	// User holds the name to authenticate as for the client. If User is empty,
	// no credentials will be sent.
	User string

	// Password holds the password for the given user, for authenticating the
	// client.
	Password string
}

// NewCharmStore creates and returns a charm store repository.
// The given parameters are used to instantiate the charm store.
//
// The errors returned from the interface methods will
// preserve the causes returned from the underlying csclient
// methods.
func NewCharmStore(p NewCharmStoreParams) *CharmStore {
	client := csclient.New(csclient.Params{
		URL:          p.URL,
		BakeryClient: p.BakeryClient,
		HTTPClient:   p.HTTPClient,
		VisitWebPage: p.VisitWebPage,
		User:         p.User,
		Password:     p.Password,
	})
	return NewCharmStoreFromClient(client)
}

// NewCharmStoreFromClient creates and returns a charm store repository.
// The provided client is used for charm store requests.
func NewCharmStoreFromClient(client *csclient.Client) *CharmStore {
	return &CharmStore{
		client: client,
	}
}

// Get implements Interface.Get.
func (s *CharmStore) Get(curl *charm.URL) (charm.Charm, error) {
	// The cache location must have been previously set.
	if CacheDir == "" {
		panic("charm cache directory path is empty")
	}
	if curl.Series == "bundle" {
		return nil, errgo.Newf("expected a charm URL, got bundle URL %q", curl)
	}
	path, err := s.archivePath(curl)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return charm.ReadCharmArchive(path)
}

// GetBundle implements Interface.GetBundle.
func (s *CharmStore) GetBundle(curl *charm.URL) (charm.Bundle, error) {
	// The cache location must have been previously set.
	if CacheDir == "" {
		panic("charm cache directory path is empty")
	}
	if curl.Series != "bundle" {
		return nil, errgo.Newf("expected a bundle URL, got charm URL %q", curl)
	}
	path, err := s.archivePath(curl)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return charm.ReadBundleArchive(path)
}

// archivePath returns a local path to the downloaded archive of the given
// charm or bundle URL, storing it in CacheDir, which it creates if necessary.
// If an archive with a matching SHA hash already exists locally, it will use
// the local version.
func (s *CharmStore) archivePath(curl *charm.URL) (string, error) {
	// Prepare the cache directory and retrieve the entity archive.
	if err := os.MkdirAll(CacheDir, 0755); err != nil {
		return "", errgo.Notef(err, "cannot create the cache directory")
	}
	etype := "charm"
	if curl.Series == "bundle" {
		etype = "bundle"
	}
	r, id, expectHash, expectSize, err := s.client.GetArchive(curl)
	if err != nil {
		if errgo.Cause(err) == params.ErrNotFound {
			// Make a prettier error message for the user.
			return "", errgo.WithCausef(nil, params.ErrNotFound, "cannot retrieve %q: %s not found", curl, etype)
		}
		return "", errgo.NoteMask(err, fmt.Sprintf("cannot retrieve %s %q", etype, curl), errgo.Any)
	}
	defer r.Close()

	// Check if the archive already exists in the cache.
	path := filepath.Join(CacheDir, charm.Quote(id.String())+"."+etype)
	if verifyHash384AndSize(path, expectHash, expectSize) == nil {
		return path, nil
	}

	// Verify and save the new archive.
	f, err := ioutil.TempFile(CacheDir, "charm-download")
	if err != nil {
		return "", errgo.Notef(err, "cannot make temporary file")
	}
	defer f.Close()
	hash := sha512.New384()
	size, err := io.Copy(io.MultiWriter(hash, f), r)
	if err != nil {
		return "", errgo.Notef(err, "cannot read entity archive")
	}
	if size != expectSize {
		return "", errgo.Newf("size mismatch; network corruption?")
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != expectHash {
		return "", errgo.Newf("hash mismatch; network corruption?")
	}

	// Move the archive to the expected place, and return the charm.

	// Note that we need to close the temporary file before moving
	// it because otherwise Windows prohibits the rename.
	f.Close()
	if err := utils.ReplaceFile(f.Name(), path); err != nil {
		return "", errgo.Notef(err, "cannot move the entity archive")
	}
	return path, nil
}

func verifyHash384AndSize(path, expectHash string, expectSize int64) error {
	f, err := os.Open(path)
	if err != nil {
		return errgo.Mask(err)
	}
	defer f.Close()
	hash := sha512.New384()
	size, err := io.Copy(hash, f)
	if err != nil {
		return errgo.Mask(err)
	}
	if size != expectSize {
		logger.Debugf("size mismatch for %q", path)
		return errgo.Newf("size mismatch for %q", path)
	}
	if fmt.Sprintf("%x", hash.Sum(nil)) != expectHash {
		logger.Debugf("hash mismatch for %q", path)
		return errgo.Newf("hash mismatch for %q", path)
	}
	return nil
}

// Latest implements Interface.Latest.
func (s *CharmStore) Latest(curls ...*charm.URL) ([]CharmRevision, error) {
	if len(curls) == 0 {
		return nil, nil
	}

	// Prepare the request to the charm store.
	urls := make([]string, len(curls))
	values := url.Values{}
	// Include the ignore-auth flag so that non-public results do not generate
	// an error for the whole request.
	values.Add("ignore-auth", "1")
	values.Add("include", "id-revision")
	values.Add("include", "hash256")
	for i, curl := range curls {
		url := curl.WithRevision(-1).String()
		urls[i] = url
		values.Add("id", url)
	}
	u := url.URL{
		Path:     "/meta/any",
		RawQuery: values.Encode(),
	}

	// Execute the request and retrieve results.
	var results map[string]struct {
		Meta struct {
			IdRevision params.IdRevisionResponse `json:"id-revision"`
			Hash256    params.HashResponse       `json:"hash256"`
		}
	}
	if err := s.client.Get(u.String(), &results); err != nil {
		return nil, errgo.NoteMask(err, "cannot get metadata from the charm store", errgo.Any)
	}

	// Build the response.
	responses := make([]CharmRevision, len(curls))
	for i, url := range urls {
		result, found := results[url]
		if !found {
			responses[i] = CharmRevision{
				Err: CharmNotFound(url),
			}
			continue
		}
		responses[i] = CharmRevision{
			Revision: result.Meta.IdRevision.Revision,
			Sha256:   result.Meta.Hash256.Sum,
		}
	}
	return responses, nil
}

// ListResources returns metadata for all the resources defined on the given charms.
func (s *CharmStore) ListResources(ids []*charm.URL) ([]ResourceResult, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	results, err := s.client.ListResources(ids)
	if err != nil {
		return nil, errgo.NoteMask(err, "cannot get resource metadata from the charm store", errgo.Any)
	}

	result := make([]ResourceResult, len(ids))
	for i, id := range ids {
		resources, ok := results[id.String()]
		if !ok {
			result[i].Err = CharmNotFound(id.String())
			continue
		}
		list := make([]resource.Resource, len(resources))
		for j, res := range resources {
			resource, err := apiResource2Resource(res)
			if err != nil {
				return nil, errgo.Notef(err, "got bad data from server for resource %q", res.Name)
			}
			list[j] = resource
		}
		result[i].Resources = list
	}
	return result, nil
}

func apiResource2Resource(res params.Resource) (resource.Resource, error) {
	var result resource.Resource
	resType, err := apiResourceType2ResourceType(res.Type)
	if err != nil {
		return result, errgo.Mask(err, errgo.Any)
	}
	origin, err := apiOrigin2Origin(res.Origin)
	if err != nil {
		return result, errgo.Mask(err, errgo.Any)
	}
	fp, err := resource.NewFingerprint(res.Fingerprint)
	if err != nil {
		return result, errgo.Mask(err, errgo.Any)
	}
	return resource.Resource{
		Meta: resource.Meta{
			Name:        res.Name,
			Type:        resType,
			Path:        res.Path,
			Description: res.Description,
		},
		Origin:      origin,
		Revision:    res.Revision,
		Fingerprint: fp,
		Size:        res.Size,
	}, nil
}

// UploadResource uploads the bytes from the given file as a resource with the given name for the charm.
func (s *CharmStore) UploadResource(id *charm.URL, name, filename string) (revision int, err error) {
	f, err := os.Open(filename)
	if err != nil {
		return -1, errgo.Mask(err, errgo.Any)
	}
	defer f.Close()
	rev, err := s.client.UploadResource(id, name, filename, f)
	if err != nil {
		return rev, errgo.Mask(err, errgo.Any)
	}
	return rev, nil
}

// ResourceData holds the information about the bytes of a resource.
type ResourceData struct {
	io.ReadCloser
	Size        int64
	Revision    int
	Fingerprint resource.Fingerprint
}

// GetLatestResource returns the bytes for the latest revision of the given resource.
func (s *CharmStore) GetLatestResource(id *charm.URL, name string) (result ResourceData, err error) {
	return s.GetResource(id, -1, name)
}

// GetResource returns the bytes for the specified revision of the given resource.
func (s *CharmStore) GetResource(id *charm.URL, revision int, name string) (result ResourceData, err error) {
	data, err := s.client.GetResource(id, revision, name)
	if err != nil {
		return result, errgo.Mask(err, errgo.Any)
	}
	defer func() {
		if err != nil {
			data.Close()
		}
	}()
	fp, err := resource.ParseFingerprint(data.Hash)
	if err != nil {
		return result, errgo.NoteMask(err, "invalid fingerprint returned from server", errgo.Any)
	}
	return ResourceData{
		ReadCloser:  data.ReadCloser,
		Size:        data.Size,
		Revision:    data.Revision,
		Fingerprint: fp,
	}, nil
}

// Publish tells the charmstore to mark the given charm as published with the
// given resource revisions to the given channels.
func (s *CharmStore) Publish(id *charm.URL, channels []string, resources map[string]int) error {
	if len(channels) == 0 {
		return errgo.New("no channel specified")
	}
	chans := make([]params.Channel, len(channels))
	for i, ch := range channels {
		chans[i] = params.Channel(ch)
	}
	val := &params.PublishRequest{
		Resources: resources,
		Channels:  chans,
	}
	if err := s.client.Put("/"+id.Path()+"/publish", val); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

func apiResourceType2ResourceType(t string) (resource.Type, error) {
	switch t {
	case "file":
		return resource.TypeFile, nil
	default:
		return 0, errgo.Newf("unknown resource type: %v", t)
	}
}

func apiOrigin2Origin(origin string) (resource.Origin, error) {
	switch origin {
	case "store":
		return resource.OriginStore, nil
	case "ulpoad":
		return resource.OriginUpload, nil
	default:
		return 0, errgo.Newf("unknown origin: %v", origin)
	}
}

// Resolve implements Interface.Resolve.
func (s *CharmStore) Resolve(ref *charm.URL) (*charm.URL, []string, error) {
	var result struct {
		Id              params.IdResponse
		SupportedSeries params.SupportedSeriesResponse
	}
	if _, err := s.client.Meta(ref, &result); err != nil {
		if errgo.Cause(err) == params.ErrNotFound {
			// Make a prettier error message for the user.
			etype := "charm"
			switch ref.Series {
			case "bundle":
				etype = "bundle"
			case "":
				etype = "charm or bundle"
			}
			return nil, nil, errgo.WithCausef(nil, params.ErrNotFound, "cannot resolve URL %q: %s not found", ref, etype)
		}
		return nil, nil, errgo.NoteMask(err, fmt.Sprintf("cannot resolve charm URL %q", ref), errgo.Any)
	}
	return result.Id.Id, result.SupportedSeries.SupportedSeries, nil
}

// URL returns the root endpoint URL of the charm store.
func (s *CharmStore) URL() string {
	return s.client.ServerURL()
}

// WithTestMode returns a repository Interface where test mode is enabled,
// meaning charm store download stats are not increased when charms are
// retrieved.
func (s *CharmStore) WithTestMode() *CharmStore {
	newRepo := *s
	newRepo.client.DisableStats()
	return &newRepo
}

// JujuMetadataHTTPHeader is the HTTP header name used to send Juju metadata
// attributes to the charm store.
const JujuMetadataHTTPHeader = "Juju-Metadata"

// WithJujuAttrs returns a repository Interface with the Juju metadata
// attributes set.
func (s *CharmStore) WithJujuAttrs(attrs map[string]string) *CharmStore {
	newRepo := *s
	header := make(http.Header)
	for k, v := range attrs {
		header.Add(JujuMetadataHTTPHeader, k+"="+v)
	}
	newRepo.client.SetHTTPHeader(header)
	return &newRepo
}

type apiClient interface {
	DisableStats()
	Do(req *http.Request, path string) (*http.Response, error)
	DoWithBody(req *http.Request, path string, body io.ReadSeeker) (*http.Response, error)
	Get(path string, result interface{}) error
	GetArchive(id *charm.URL) (r io.ReadCloser, eid *charm.URL, hash string, size int64, err error)
	GetResource(id *charm.URL, revision int, name string) (csclient.ResourceData, error)
	ListResources(ids []*charm.URL) (map[string][]params.Resource, error)
	Log(typ params.LogType, level params.LogLevel, message string, urls ...*charm.URL) error
	Login() error
	Meta(id *charm.URL, result interface{}) (*charm.URL, error)
	Put(path string, val interface{}) error
	PutCommonInfo(id *charm.URL, info map[string]interface{}) error
	PutExtraInfo(id *charm.URL, info map[string]interface{}) error
	PutWithResponse(path string, val, result interface{}) error
	ServerURL() string
	SetHTTPHeader(header http.Header)
	StatsUpdate(req params.StatsUpdateRequest) error
	UploadBundle(id *charm.URL, b charm.Bundle) (*charm.URL, error)
	UploadBundleWithRevision(id *charm.URL, b charm.Bundle, promulgatedRevision int) error
	UploadCharm(id *charm.URL, ch charm.Charm) (*charm.URL, error)
	UploadCharmWithRevision(id *charm.URL, ch charm.Charm, promulgatedRevision int) error
	UploadResource(id *charm.URL, name, path string, file io.ReadSeeker) (revision int, err error)
	WhoAmI() (*params.WhoAmIResponse, error)
}
