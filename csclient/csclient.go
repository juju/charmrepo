// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

// The csclient package provides access to the charm store API.
//
// Errors returned from the remote API server with an associated error
// code will have a cause of type params.ErrorCode holding that code.
//
// If a call to the API returns an error because authorization has been
// denied, an error with a cause satisfying IsAuthorizationError will be
// returned. Note that these errors can also include errors returned by
// httpbakery when it attempts to discharge macaroons.
package csclient // import "gopkg.in/juju/charmrepo.v4/csclient"

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/errgo.v1"
	httprequest "gopkg.in/httprequest.v1"
	"gopkg.in/juju/charm.v6"
	"gopkg.in/macaroon-bakery.v2/httpbakery"

	"gopkg.in/juju/charmrepo.v4/csclient/params"
)

const apiVersion = "v5"

const defaultMinMultipartUploadSize = 5 * 1024 * 1024

// ServerURL holds the default location of the global charm store.
// An alternate location can be configured by changing the URL field in the
// Params struct.
// For live testing or QAing the application, a different charm store
// location should be used, for instance "https://api.staging.jujucharms.com".
var ServerURL = "https://api.jujucharms.com/charmstore"

// Client represents the client side of a charm store.
type Client struct {
	params                 Params
	bclient                httpClient
	header                 http.Header
	statsDisabled          bool
	channel                params.Channel
	minMultipartUploadSize int64
}

// Params holds parameters for creating a new charm store client.
type Params struct {
	// URL holds the root endpoint URL of the charmstore,
	// with no trailing slash, not including the version.
	// For example https://api.jujucharms.com/charmstore
	// If empty, the default charm store client location is used.
	URL string

	// User holds the name to authenticate as for the client. If User is empty,
	// no credentials will be sent.
	User string

	// Password holds the password for the given user, for authenticating the
	// client.
	Password string

	// BakeryClient holds the bakery client to use when making
	// requests to the store. This is used in preference to
	// HTTPClient.
	BakeryClient *httpbakery.Client
}

type httpClient interface {
	Do(*http.Request) (*http.Response, error)
}

// New returns a new charm store client.
func New(p Params) *Client {
	if p.URL == "" {
		p.URL = ServerURL
	}
	bclient := p.BakeryClient
	if bclient == nil {
		bclient = httpbakery.NewClient()
		bclient.AddInteractor(httpbakery.WebBrowserInteractor{})
	}
	return &Client{
		bclient:                bclient,
		params:                 p,
		minMultipartUploadSize: defaultMinMultipartUploadSize,
	}
}

// SetMinMultipartUploadSize sets the minimum size of resource upload
// that will trigger a multipart upload. This is mainly useful for testing.
func (c *Client) SetMinMultipartUploadSize(n int64) {
	c.minMultipartUploadSize = n
}

// ServerURL returns the charm store URL used by the client.
func (c *Client) ServerURL() string {
	return c.params.URL
}

// DisableStats disables incrementing download stats when retrieving archives
// from the charm store.
func (c *Client) DisableStats() {
	c.statsDisabled = true
}

// WithChannel returns a new client whose requests are done using the
// given channel.
func (c *Client) WithChannel(channel params.Channel) *Client {
	client := *c
	client.channel = channel
	return &client
}

// Channel returns the currently set channel.
func (c *Client) Channel() params.Channel {
	return c.channel
}

// SetHTTPHeader sets custom HTTP headers that will be sent to the charm store
// on each request.
func (c *Client) SetHTTPHeader(header http.Header) {
	c.header = header
}

// GetArchive retrieves the archive for the given charm or bundle, returning a
// reader its data can be read from, the fully qualified id of the
// corresponding entity, the hex-encoded SHA384 hash of the data and its size.
func (c *Client) GetArchive(id *charm.URL) (r io.ReadCloser, eid *charm.URL, hash string, size int64, err error) {
	fail := func(err error) (io.ReadCloser, *charm.URL, string, int64, error) {
		return nil, nil, "", 0, err
	}
	// Create the request.
	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		return fail(errgo.Notef(err, "cannot make new request"))
	}

	// Send the request.
	v := url.Values{}
	if c.statsDisabled {
		v.Set("stats", "0")
	}
	u := url.URL{
		Path:     "/" + id.Path() + "/archive",
		RawQuery: v.Encode(),
	}
	resp, err := c.Do(req, u.String())
	if err != nil {
		terr := params.MaybeTermsAgreementError(err)
		if err1, ok := errgo.Cause(terr).(*params.TermAgreementRequiredError); ok {
			terms := strings.Join(err1.Terms, " ")
			return fail(errgo.Newf(`cannot get archive because some terms have not been agreed to. Try "juju agree %s"`, terms))
		}
		return fail(errgo.NoteMask(err, "cannot get archive", isAPIError))
	}

	// Validate the response headers.
	entityId := resp.Header.Get(params.EntityIdHeader)
	if entityId == "" {
		resp.Body.Close()
		return fail(errgo.Newf("no %s header found in response", params.EntityIdHeader))
	}
	eid, err = charm.ParseURL(entityId)
	if err != nil {
		// The server did not return a valid id.
		resp.Body.Close()
		return fail(errgo.Notef(err, "invalid entity id found in response"))
	}
	if eid.Revision == -1 {
		// The server did not return a fully qualified entity id.
		resp.Body.Close()
		return fail(errgo.Newf("archive get returned not fully qualified entity id %q", eid))
	}
	hash = resp.Header.Get(params.ContentHashHeader)
	if hash == "" {
		resp.Body.Close()
		return fail(errgo.Newf("no %s header found in response", params.ContentHashHeader))
	}

	// Validate the response contents.
	if resp.ContentLength < 0 {
		// TODO frankban: handle the case the contents are chunked.
		resp.Body.Close()
		return fail(errgo.Newf("no content length found in response"))
	}
	return resp.Body, eid, hash, resp.ContentLength, nil
}

// ListResources retrieves the metadata about resources for the given charms.
// It returns a slice with an element for each of the given ids, holding the
// resources for the respective id.
func (c *Client) ListResources(id *charm.URL) ([]params.Resource, error) {
	var result []params.Resource
	if err := c.Get("/"+id.Path()+"/meta/resources", &result); err != nil {
		return nil, errgo.NoteMask(err, "cannot get resource metadata from the charm store", isAPIError)
	}
	return result, nil
}

// Progress lets an upload notify a caller about the progress of the upload.
type Progress interface {
	// Start is called with the upload id when the upload starts.
	// The upload id will be empty when multipart upload is not
	// being used (when the upload is small or the server does not
	// support multipart upload).
	Start(uploadId string, expires time.Time)

	// Transferred is called periodically to notify the caller that
	// the given number of bytes have been uploaded. Note that the
	// number may decrease - for example when most of a file has
	// been transferred before a network error occurs.
	Transferred(total int64)

	// Error is called when a non-fatal error (any non-API error) has
	// been encountered when uploading.
	Error(err error)

	// Finalizing is called when all the parts of a multipart upload
	// are being stitched together into the final resource.
	// This will not be called if the upload is not split into
	// multiple parts.
	Finalizing()
}

// UploadResource uploads the contents of a resource of the given name
// attached to a charm with the given id. The given path will be used as
// the resource path metadata and the contents will be read from the
// given file, which must have the given size. If progress is not nil, it will
// be called to inform the caller of the progress of the upload.
func (c *Client) UploadResource(id *charm.URL, name, path string, file io.ReaderAt, size int64, progress Progress) (revision int, err error) {
	return c.ResumeUploadResource("", id, name, path, file, size, progress)
}

// AddDockerResource adds a reference to a docker image that is available in a docker
// registry as a resource to the charm with the given id. If imageName is non-empty,
// it names the image in some non-charmstore-associated registry; otherwise
// the image should have been uploaded to the charmstore-associated registry
// (see DockerResourceUploadInfo for details on how to do that).
// The digest should hold the digest of the image (in "sha256:hex" format).
//
// AddDockerResource returns the revision of the newly added resource.
func (c *Client) AddDockerResource(id *charm.URL, resourceName string, imageName, digest string) (revision int, err error) {
	path := fmt.Sprintf("/%s/resource/%s", id.Path(), resourceName)
	var result params.ResourceUploadResponse
	if err := c.DoWithResponse("POST", path, params.DockerResourceUploadRequest{
		Digest:    digest,
		ImageName: imageName,
	}, &result); err != nil {
		return 0, errgo.Mask(err)
	}
	return result.Revision, nil
}

// DockerResourceDownloadInfo returns information on how
// to download the given resource in the given Kubernetes charm
// from a docker registry. The returned information
// includes the image name to use and the username and password
// to use for authentication.
func (c *Client) DockerResourceDownloadInfo(id *charm.URL, resourceName string) (*params.DockerInfoResponse, error) {
	path := fmt.Sprintf("/%s/resource/%s", id.Path(), resourceName)
	var result params.DockerInfoResponse
	if err := c.Get(path, &result); err != nil {
		return nil, errgo.Mask(err)
	}
	return &result, nil
}

// DockerResourceUploadInfo returns information on how to upload an image
// to the charm store's associated docker registry.
// The returned information includes a tag to associate with the image
// and username and password to use for push authentication.
func (c *Client) DockerResourceUploadInfo(id *charm.URL, resourceName string) (*params.DockerInfoResponse, error) {
	path := fmt.Sprintf("/%s/docker-resource-upload-info?resource-name=%s", id.Path(), url.QueryEscape(resourceName))
	var result params.DockerInfoResponse
	if err := c.DoWithResponse("GET", path, nil, &result); err != nil {
		return nil, errgo.Mask(err)
	}
	return &result, nil
}

var ErrUploadNotFound = errgo.Newf("upload not found")

// ResumeUploadResource is like UploadResource except that if uploadId is non-empty,
// it specifies the id of an existing upload to resume; if an upload with this ID is not
// found, an error with an ErrUploadNotFound cause is returned.
func (c *Client) ResumeUploadResource(uploadId string, id *charm.URL, resourceName, path string, content io.ReaderAt, size int64, progress Progress) (revision int, err error) {
	if progress == nil {
		progress = noProgress{}
	}
	info := &uploadInfo{
		id:           id,
		resourceName: resourceName,
		path:         path,
		size:         size,
		progress:     progress,
		content:      content,
	}
	if size >= c.minMultipartUploadSize {
		return c.uploadMultipartResource(uploadId, info)
	}
	return c.uploadSinglePartResource(info)
}

func (c *Client) uploadSinglePartResource(info *uploadInfo) (revision int, err error) {
	info.progress.Start("", time.Time{})
	hash, size1, err := readerHashAndSize(io.NewSectionReader(info.content, 0, info.size))
	if err != nil {
		return -1, errgo.Mask(err)
	}
	if size1 != info.size {
		return 0, errgo.Newf("resource file changed underfoot? (initial size %d, then %d)", info.size, size1)
	}
	// Prepare the request.
	req, err := http.NewRequest("POST", "", newProgressReader(io.NewSectionReader(info.content, 0, info.size), info.progress, 0))
	if err != nil {
		return 0, errgo.Notef(err, "cannot make new request")
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = info.size
	url := fmt.Sprintf("/%s/resource/%s?hash=%s&filename=%s", info.id.Path(), info.resourceName, url.QueryEscape(hash), url.QueryEscape(info.path))
	resp, err := c.Do(req, url)
	if err != nil {
		return 0, errgo.NoteMask(err, "cannot post resource", isAPIError)
	}
	defer resp.Body.Close()

	// Parse the response.
	var result params.ResourceUploadResponse
	if err := httprequest.UnmarshalJSONResponse(resp, &result); err != nil {
		return 0, errgo.Mask(err)
	}
	return result.Revision, nil
}

type uploadInfo struct {
	id           *charm.URL
	resourceName string
	path         string
	size         int64
	progress     Progress
	content      io.ReaderAt

	// The following fields are only set for multipart uploads.
	params.UploadInfoResponse
	preferredPartSize int64
}

func (c *Client) uploadMultipartResource(uploadId string, info *uploadInfo) (int, error) {
	if uploadId == "" {
		// Create the upload.
		if err := c.DoWithResponse("POST", "/upload", nil, &info.UploadInfoResponse); err != nil {
			if errgo.Cause(err) == params.ErrNotFound {
				// An earlier version of the API - try single part upload even though it's big.
				return c.uploadSinglePartResource(info)
			}
			return 0, errgo.Mask(err)
		}
	} else {
		if err := c.DoWithResponse("GET", "/upload/"+uploadId, nil, &info.UploadInfoResponse); err != nil {
			if errgo.Cause(err) == params.ErrNotFound {
				return 0, errgo.WithCausef(nil, ErrUploadNotFound, "")
			}
			return 0, errgo.Mask(err)
		}
		if info.UploadId != uploadId {
			return 0, errgo.Newf("unexpected upload id in response (got %q want %q)", info.UploadId, uploadId)
		}
	}
	info.progress.Start(info.UploadId, info.Expires)
	// Calculate the part size, but round up so that we have
	// enough parts to cover the remainder at the end.
	info.preferredPartSize = (info.size + int64(info.MaxParts) - 1) / int64(info.MaxParts)
	if info.preferredPartSize > info.MaxPartSize {
		return 0, errgo.Newf("resource too big (allowed %.3fGB)", float64(info.MaxPartSize)*float64(info.MaxParts)/1e9)
	}
	if info.preferredPartSize < info.MinPartSize {
		info.preferredPartSize = info.MinPartSize
	}
	revision, err := c.uploadParts(info)
	if err != nil {
		return 0, errgo.Mask(err)
	}
	return revision, nil
}

func (c *Client) uploadParts(info *uploadInfo) (int, error) {
	parts := info.Parts
	offset := int64(0)
loop:
	for i := 0; offset < info.size; i++ {
		p0, p1, err := choosePartRange(i, offset, info)
		offset = p1
		if err != nil {
			switch errgo.Cause(err) {
			case errAlreadyUploaded:
				info.progress.Transferred(p1)
				continue
			case errFinished:
				break loop
			default:
				return 0, errgo.Mask(err)
			}
		}
		// TODO concurrent part upload?
		hash, err := c.uploadPart(info.UploadId, i, info.content, p0, p1, info.progress)
		if err != nil {
			return 0, errgo.Mask(err)
		}
		part := params.Part{
			Hash:     hash,
			Complete: true,
		}
		if i < len(parts.Parts) {
			parts.Parts[i] = part
		} else {
			// We can just append to parts because we know that if i >= len(parts.Parts),
			// we always call uploadPart and append to parts.Parts, because choosePartRange
			// will never return errAlreadyUploaded for a nonexistent part.
			parts.Parts = append(parts.Parts, part)
		}
	}
	info.progress.Finalizing()
	// All parts uploaded, now complete the upload.
	var finishResponse params.FinishUploadResponse
	if err := c.PutWithResponse("/upload/"+info.UploadId, parts, &finishResponse); err != nil {
		return 0, errgo.Mask(err)
	}
	url := fmt.Sprintf("/%s/resource/%s?upload-id=%s&filename=%s", info.id.Path(), info.resourceName, info.UploadId, info.path)

	// The multipart upload has now been uploaded.
	// Create the resource that uses it.
	var resourceResp params.ResourceUploadResponse
	if err := c.DoWithResponse("POST", url, nil, &resourceResp); err != nil {
		return -1, errgo.NoteMask(err, "cannot post resource", isAPIError)
	}
	return resourceResp.Revision, nil
}

var (
	errAlreadyUploaded = errgo.Newf("resource part already uploaded")
	errFinished        = errgo.Newf("all resource parts uploaded")
)

// choosePartRange returns the file range to use for the part with the given index.
// It returns errAlreadyUploaded if the part is complete and errFinished if the part is
// at the end.
func choosePartRange(partIndex int, offset int64, info *uploadInfo) (p0, p1 int64, err error) {
	if offset >= info.size {
		return info.size, info.size, errFinished
	}
	if partIndex < len(info.Parts.Parts) {
		if part := info.Parts.Parts[partIndex]; part.Complete {
			if part.Offset != offset {
				return 0, 0, errgo.Newf("offset mismatch at part %d (want %d got %d)", partIndex, offset, part.Offset)
			}
			return offset, offset + part.Size, errAlreadyUploaded
		}
	}

	nextOffset := info.size
	nextUploadedPart := -1
	// Find the offset of the next uploaded part, if any.
	for i := partIndex + 1; i < len(info.Parts.Parts); i++ {
		if info.Parts.Parts[i].Valid() {
			nextOffset = info.Parts.Parts[i].Offset
			nextUploadedPart = i
			break
		}
	}
	if nextUploadedPart == partIndex+1 {
		// Exactly one part to fill in.
		p0, p1 = offset, nextOffset
		if p1-p0 < info.MinPartSize {
			return 0, 0, errgo.Newf("remaining part is too small")
		}
		if p1-p0 > info.MaxPartSize {
			return 0, 0, errgo.Newf("remaining part is too large")
		}
		return p0, p1, nil
	}
	if nextUploadedPart == -1 {
		// No next part, so we can choose for ourselves.
		p0 = offset
		p1 = offset + info.preferredPartSize
		if p1 > info.size {
			p1 = info.size
		}
		return p0, p1, nil
	}
	// There's an already-uploaded part more than one away, so
	// divide it equally (rounding errors will be allocated to the last
	// part, which should be dealt with by the "exactly one part" case
	// above).
	partSize := (nextOffset - offset) / int64(nextUploadedPart-partIndex)
	return offset, offset + partSize, nil
}

// progressReader implements an io.Reader that informs a Progress
// implementation of progress as data is transferred. Note that this
// will not work correctly if two uploads are made concurrently.
type progressReader struct {
	r     io.ReadSeeker
	p     Progress
	pos   int64
	start int64
}

// newProgressReader returns a reader that reads from r and calls
// p.Transferred with the number of bytes that have been transferred.
// The start parameter holds the number of bytes that have already been
// transferred.
func newProgressReader(r io.ReadSeeker, p Progress, start int64) io.ReadSeeker {
	return &progressReader{
		r:     r,
		p:     p,
		pos:   start,
		start: start,
	}
}

// Read implements io.Reader.Read.
func (p *progressReader) Read(pb []byte) (int, error) {
	n, err := p.r.Read(pb)
	if n > 0 {
		p.pos += int64(n)
		p.p.Transferred(p.pos)
	}
	return n, err
}

// Seek implements io.Seeker.Seek.
func (p *progressReader) Seek(offset int64, whence int) (int64, error) {
	pos, err := p.r.Seek(offset, whence)
	p.pos = p.start + pos
	p.p.Transferred(p.pos)
	return pos, err
}

// uploadPart uploads a single part of a multipart upload
// and returns the hash of the part.
func (c *Client) uploadPart(uploadId string, part int, r io.ReaderAt, p0, p1 int64, progress Progress) (string, error) {
	h := sha512.New384()
	if _, err := io.Copy(h, io.NewSectionReader(r, p0, p1-p0)); err != nil {
		return "", errgo.Notef(err, "cannot read resource")
	}
	hash := fmt.Sprintf("%x", h.Sum(nil))
	var lastError error
	section := newProgressReader(io.NewSectionReader(r, p0, p1-p0), progress, p0)
	for i := 0; i < 10; i++ {
		req, err := http.NewRequest("PUT", "", section)
		if err != nil {
			return "", errgo.Mask(err)
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = p1 - p0
		resp, err := c.Do(req, fmt.Sprintf("/upload/%s/%d?hash=%s&offset=%d", uploadId, part, hash, p0))
		if err == nil {
			// Success
			resp.Body.Close()
			return hash, nil
		}
		if isAPIError(err) {
			// It's a genuine error from the charm store, so
			// stop trying.
			return "", errgo.Mask(err, isAPIError)
		}
		progress.Error(err)
		lastError = err
		section.Seek(0, 0)
		// Try again.
	}
	return "", errgo.Notef(lastError, "too many attempts; last error")
}

// Publish tells the charmstore to mark the given charm as published with the
// given resource revisions to the given channels.
func (c *Client) Publish(id *charm.URL, channels []params.Channel, resources map[string]int) error {
	if len(channels) == 0 {
		return nil
	}
	val := &params.PublishRequest{
		Resources: resources,
		Channels:  channels,
	}
	if err := c.Put("/"+id.Path()+"/publish", val); err != nil {
		return errgo.Mask(err, isAPIError)
	}
	return nil
}

// ResourceData holds information about a resource.
// It must be closed after use.
type ResourceData struct {
	io.ReadCloser
	Hash string
}

// GetResource retrieves byes of the resource with the given name and revision
// for the given charm, returning a reader its data can be read from,  the
// SHA384 hash of the data.
//
// Note that the result must be closed after use.
func (c *Client) GetResource(id *charm.URL, name string, revision int) (result ResourceData, err error) {
	if revision < 0 {
		return result, errgo.New("revision must be a non-negative integer")
	}
	// Create the request.
	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		return result, errgo.Notef(err, "cannot make new request")
	}

	url := "/" + id.Path() + "/resource/" + name
	if revision >= 0 {
		url += "/" + strconv.Itoa(revision)
	}
	resp, err := c.Do(req, url)
	if err != nil {
		return result, errgo.NoteMask(err, "cannot get resource", isAPIError)
	}
	defer func() {
		if err != nil {
			resp.Body.Close()
		}
	}()

	// Validate the response headers.
	hash := resp.Header.Get(params.ContentHashHeader)
	if hash == "" {
		return result, errgo.Newf("no %s header found in response", params.ContentHashHeader)
	}

	return ResourceData{
		ReadCloser: resp.Body,
		Hash:       hash,
	}, nil
}

// ResourceMeta returns the metadata for the resource on charm id with the
// given name and revision. If the revision is negative, the latest version
// of the resource will be returned.
func (c *Client) ResourceMeta(id *charm.URL, name string, revision int) (params.Resource, error) {
	path := fmt.Sprintf("/%s/meta/resources/%s", id.Path(), name)
	if revision >= 0 {
		path += fmt.Sprintf("/%d", revision)
	}
	var result params.Resource
	if err := c.Get(path, &result); err != nil {
		return result, errgo.NoteMask(err, fmt.Sprintf("cannot get %q", path), isAPIError)
	}
	return result, nil
}

// StatsUpdate updates the download stats for the given id and specific time.
func (c *Client) StatsUpdate(req params.StatsUpdateRequest) error {
	return c.Put("/stats/update", req)
}

// UploadCharm uploads the given charm to the charm store with the given id,
// which must not specify a revision.
// The accepted charm implementations are charm.CharmDir and
// charm.CharmArchive.
//
// UploadCharm returns the id that the charm has been given in the
// store - this will be the same as id except the revision.
func (c *Client) UploadCharm(id *charm.URL, ch charm.Charm) (*charm.URL, error) {
	if id.Revision != -1 {
		return nil, errgo.Newf("revision specified in %q, but should not be specified", id)
	}
	r, hash, size, err := openArchive(ch)
	if err != nil {
		return nil, errgo.Notef(err, "cannot open charm archive")
	}
	defer r.Close()
	return c.uploadArchive(id, r, hash, size, -1)
}

// UploadCharmWithRevision uploads the given charm to the
// given id in the charm store, which must contain a revision.
// If promulgatedRevision is not -1, it specifies that the charm
// should be marked as promulgated with that revision.
//
// This method is provided only for testing and should not
// generally be used otherwise.
func (c *Client) UploadCharmWithRevision(id *charm.URL, ch charm.Charm, promulgatedRevision int) error {
	if id.Revision == -1 {
		return errgo.Newf("revision not specified in %q", id)
	}
	r, hash, size, err := openArchive(ch)
	if err != nil {
		return errgo.Notef(err, "cannot open charm archive")
	}
	defer r.Close()
	_, err = c.uploadArchive(id, r, hash, size, promulgatedRevision)
	return errgo.Mask(err, isAPIError)
}

// UploadBundle uploads the given charm to the charm store with the given id,
// which must not specify a revision.
// The accepted bundle implementations are charm.BundleDir and
// charm.BundleArchive.
//
// UploadBundle returns the id that the bundle has been given in the
// store - this will be the same as id except the revision.
func (c *Client) UploadBundle(id *charm.URL, b charm.Bundle) (*charm.URL, error) {
	if id.Revision != -1 {
		return nil, errgo.Newf("revision specified in %q, but should not be specified", id)
	}
	r, hash, size, err := openArchive(b)
	if err != nil {
		return nil, errgo.Notef(err, "cannot open bundle archive")
	}
	defer r.Close()
	return c.uploadArchive(id, r, hash, size, -1)
}

// UploadBundleWithRevision uploads the given bundle to the
// given id in the charm store, which must contain a revision.
// If promulgatedRevision is not -1, it specifies that the charm
// should be marked as promulgated with that revision.
//
// This method is provided only for testing and should not
// generally be used otherwise.
func (c *Client) UploadBundleWithRevision(id *charm.URL, b charm.Bundle, promulgatedRevision int) error {
	if id.Revision == -1 {
		return errgo.Newf("revision not specified in %q", id)
	}
	r, hash, size, err := openArchive(b)
	if err != nil {
		return errgo.Notef(err, "cannot open charm archive")
	}
	defer r.Close()
	_, err = c.uploadArchive(id, r, hash, size, promulgatedRevision)
	return errgo.Mask(err, isAPIError)
}

// uploadArchive pushes the archive for the charm or bundle represented by
// the given body, its hex-encoded SHA384 hash and its size. It returns
// the resulting entity reference. The given id should include the series
// and should not include the revision.
func (c *Client) uploadArchive(id *charm.URL, body io.ReadSeeker, hash string, size int64, promulgatedRevision int) (*charm.URL, error) {
	// When uploading archives, it can be a problem that the
	// an error response is returned while we are still writing
	// the body data.
	// To avoid this, we log in first so that we don't need to
	// do the macaroon exchange after POST.
	// Unfortunately this won't help matters if the user is logged in but
	// doesn't have privileges to write to the stated charm.
	// A better solution would be to fix https://github.com/golang/go/issues/3665
	// and use the 100-Continue client functionality.
	//
	// We only need to do this when basic auth credentials are not provided.
	if c.params.User == "" {
		if err := c.Login(); err != nil {
			return nil, errgo.NoteMask(err, "cannot log in", isAPIError)
		}
	}
	method := "POST"
	promulgatedArg := ""
	if id.Revision != -1 {
		method = "PUT"
		if promulgatedRevision != -1 {
			pr := *id
			pr.User = ""
			pr.Revision = promulgatedRevision
			promulgatedArg = "&promulgated=" + pr.Path()
		}
	}

	// Prepare the request.
	req, err := http.NewRequest(method, "", body)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make new request")
	}
	req.Header.Set("Content-Type", "application/zip")
	req.ContentLength = size

	// Send the request.
	resp, err := c.Do(
		req,
		"/"+id.Path()+"/archive?hash="+hash+promulgatedArg,
	)
	if err != nil {
		return nil, errgo.NoteMask(err, "cannot post archive", isAPIError)
	}
	defer resp.Body.Close()

	// Parse the response.
	var result params.ArchiveUploadResponse
	if err := httprequest.UnmarshalJSONResponse(resp, &result); err != nil {
		return nil, errgo.NoteMask(err, "cannot unmarshal response", errgo.Any)
	}
	return result.Id, nil
}

// PutExtraInfo puts extra-info data for the given id.
// Each entry in the info map causes a value in extra-info with
// that key to be set to the associated value.
// Entries not set in the map will be unchanged.
func (c *Client) PutExtraInfo(id *charm.URL, info map[string]interface{}) error {
	return c.Put("/"+id.Path()+"/meta/extra-info", info)
}

// PutCommonInfo puts common-info data for the given id.
// Each entry in the info map causes a value in common-info with
// that key to be set to the associated value.
// Entries not set in the map will be unchanged.
func (c *Client) PutCommonInfo(id *charm.URL, info map[string]interface{}) error {
	return c.Put("/"+id.Path()+"/meta/common-info", info)
}

// Meta fetches metadata on the charm or bundle with the
// given id. The result value provides a value
// to be filled in with the result, which must be
// a pointer to a struct containing members corresponding
// to possible metadata include parameters
// (see https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmeta).
//
// It returns the fully qualified id of the entity.
//
// The name of the struct member is translated to
// a lower case hyphen-separated form; for example,
// ArchiveSize becomes "archive-size", and BundleMachineCount
// becomes "bundle-machine-count", but may also
// be specified in the field's tag
//
// This example will fill in the result structure with information
// about the given id, including information on its archive
// size (include archive-size), upload time (include archive-upload-time)
// and digest (include extra-info/digest).
//
//	var result struct {
//		ArchiveSize params.ArchiveSizeResponse
//		ArchiveUploadTime params.ArchiveUploadTimeResponse
//		Digest string `csclient:"extra-info/digest"`
//	}
//	id, err := client.Meta(id, &result)
func (c *Client) Meta(id *charm.URL, result interface{}) (*charm.URL, error) {
	if result == nil {
		return nil, fmt.Errorf("expected valid result pointer, not nil")
	}
	resultv := reflect.ValueOf(result)
	resultt := resultv.Type()
	if resultt.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("expected pointer, not %T", result)
	}
	resultt = resultt.Elem()
	if resultt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected pointer to struct, not %T", result)
	}
	resultv = resultv.Elem()

	// At this point, resultv refers to the struct value pointed
	// to by result, and resultt is its type.

	numField := resultt.NumField()
	includes := make([]string, 0, numField)

	// results holds an entry for each field in the result value,
	// pointing to the value for that field.
	results := make(map[string]reflect.Value)
	for i := 0; i < numField; i++ {
		field := resultt.Field(i)
		if field.PkgPath != "" {
			// Field is private; ignore it.
			continue
		}
		if field.Anonymous {
			// At some point in the future, it might be nice to
			// support anonymous fields, but for now the
			// additional complexity doesn't seem worth it.
			return nil, fmt.Errorf("anonymous fields not supported")
		}
		apiName := field.Tag.Get("csclient")
		if apiName == "" {
			apiName = hyphenate(field.Name)
		}
		includes = append(includes, "include="+apiName)
		results[apiName] = resultv.FieldByName(field.Name).Addr()
	}
	// We unmarshal into rawResult, then unmarshal each field
	// separately into its place in the final result value.
	// Note that we can't use params.MetaAnyResponse because
	// that will unpack all the values inside the Meta field,
	// but we want to keep them raw so that we can unmarshal
	// them ourselves.
	var rawResult struct {
		Id   *charm.URL
		Meta map[string]json.RawMessage
	}
	path := "/" + id.Path() + "/meta/any"
	if len(includes) > 0 {
		path += "?" + strings.Join(includes, "&")
	}
	if err := c.Get(path, &rawResult); err != nil {
		return nil, errgo.NoteMask(err, fmt.Sprintf("cannot get %q", path), isAPIError)
	}
	// Note that the server is not required to send back values
	// for all fields. "If there is no metadata for the given meta path, the
	// element will be omitted"
	// See https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaany
	for name, r := range rawResult.Meta {
		v, ok := results[name]
		if !ok {
			// The server has produced a result that we
			// don't know about. Ignore it.
			continue
		}
		// Unmarshal the raw JSON into the final struct field.
		err := json.Unmarshal(r, v.Interface())
		if err != nil {
			return nil, errgo.Notef(err, "cannot unmarshal %s", name)
		}
	}
	return rawResult.Id, nil
}

// hyphenate returns the hyphenated version of the given
// field name, as specified in the Client.Meta method.
func hyphenate(s string) string {
	// TODO hyphenate FooHTTPBar as foo-http-bar?
	var buf bytes.Buffer
	var prevLower bool
	for _, r := range s {
		if !unicode.IsUpper(r) {
			prevLower = true
			buf.WriteRune(r)
			continue
		}
		if prevLower {
			buf.WriteRune('-')
		}
		buf.WriteRune(unicode.ToLower(r))
		prevLower = false
	}
	return buf.String()
}

// Get makes a GET request to the given path in the charm store (not
// including the host name or version prefix but including a leading /),
// parsing the result as JSON into the given result value, which should
// be a pointer to the expected data, but may be nil if no result is
// desired.
func (c *Client) Get(path string, result interface{}) error {
	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		return errgo.Notef(err, "cannot make new request")
	}
	resp, err := c.Do(req, path)
	if err != nil {
		return errgo.Mask(err, isAPIError)
	}
	defer resp.Body.Close()
	// Parse the response.
	if err := httprequest.UnmarshalJSONResponse(resp, result); err != nil {
		return errgo.Notef(err, "cannot unmarshal response")
	}
	return nil
}

// Put makes a PUT request to the given path in the charm store
// (not including the host name or version prefix, but including a leading /),
// marshaling the given value as JSON to use as the request body.
func (c *Client) Put(path string, val interface{}) error {
	return c.PutWithResponse(path, val, nil)
}

// PutWithResponse makes a PUT request to the given path in the charm store
// (not including the host name or version prefix, but including a leading /),
// marshaling the given value as JSON to use as the request body. Additionally,
// this method parses the result as JSON into the given result value, which
// should be a pointer to the expected data, but may be nil if no result is
// desired.
func (c *Client) PutWithResponse(path string, val, result interface{}) error {
	return c.DoWithResponse("PUT", path, val, result)
}

// DoWithResponse is more general version of PutWithResponse. It performs
// the given HTTP method on the given charm store path, sending
// val as the JSON request body and unmarshaling the JSON response into result.
func (c *Client) DoWithResponse(method string, path string, val, result interface{}) error {
	data, err := json.Marshal(val)
	if err != nil {
		return errgo.Notef(err, "cannot marshal PUT body")
	}
	req, _ := http.NewRequest(method, "", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req, path)
	if err != nil {
		return errgo.Mask(err, isAPIError)
	}
	defer resp.Body.Close()
	// Parse the response.
	if err := httprequest.UnmarshalJSONResponse(resp, result); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

// Do makes an arbitrary request to the charm store.
// It adds appropriate headers to the given HTTP request,
// sends it to the charm store, and returns the resulting
// response. Do never returns a response with a status
// that is not http.StatusOK.
//
// The URL field in the request is ignored and overwritten.
//
// This is a low level method - more specific Client methods
// should be used when possible.
//
// Note that if a body is supplied in the request, it should
// implement io.Seeker.
//
// Any error returned from the underlying httpbakery.Do
// request will have an unchanged error cause.
func (c *Client) Do(req *http.Request, path string) (*http.Response, error) {
	if c.params.User != "" {
		userPass := c.params.User + ":" + c.params.Password
		authBasic := base64.StdEncoding.EncodeToString([]byte(userPass))
		req.Header.Set("Authorization", "Basic "+authBasic)
	}

	// Prepare the request.
	if !strings.HasPrefix(path, "/") {
		return nil, errgo.Newf("path %q is not absolute", path)
	}
	for k, vv := range c.header {
		req.Header[k] = append(req.Header[k], vv...)
	}
	u, err := url.Parse(c.params.URL + "/" + apiVersion + path)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	if c.channel != params.NoChannel {
		values := u.Query()
		values.Set("channel", string(c.channel))
		u.RawQuery = values.Encode()
	}
	req.URL = u

	// Send the request.
	resp, err := c.bclient.Do(req)
	if err != nil {
		return nil, errgo.Mask(err, isAPIError)
	}

	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()

	// Parse the response error.
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errgo.Notef(err, "cannot read response body")
	}

	if resp.Header.Get("Content-Type") != "application/json" {
		return nil, errgo.Newf("unexpected response status from server: %v", resp.Status)
	}
	var perr params.Error
	if err := json.Unmarshal(data, &perr); err != nil {
		return nil, errgo.Notef(err, "cannot unmarshal error response %q", sizeLimit(data))
	}
	if perr.Message == "" {
		return nil, errgo.Newf("error response with empty message %s", sizeLimit(data))
	}
	return nil, &perr
}

func sizeLimit(data []byte) []byte {
	const max = 1024
	if len(data) < max {
		return data
	}
	return append(data[0:max], fmt.Sprintf(" ... [%d bytes omitted]", len(data)-max)...)
}

// Log sends a log message to the charmstore's log database.
func (cs *Client) Log(typ params.LogType, level params.LogLevel, message string, urls ...*charm.URL) error {
	b, err := json.Marshal(message)
	if err != nil {
		return errgo.Notef(err, "cannot marshal log message")
	}

	// Prepare and send the log.
	// TODO (frankban): we might want to buffer logs in order to reduce
	// requests.
	logs := []params.Log{{
		Data:  (*json.RawMessage)(&b),
		Level: level,
		Type:  typ,
		URLs:  urls,
	}}
	b, err = json.Marshal(logs)
	if err != nil {
		return errgo.Notef(err, "cannot marshal log message")
	}

	req, err := http.NewRequest("POST", "", bytes.NewReader(b))
	if err != nil {
		return errgo.Notef(err, "cannot create log request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := cs.Do(req, "/log")
	if err != nil {
		return errgo.NoteMask(err, "cannot send log message", isAPIError)
	}
	resp.Body.Close()
	return nil
}

// Login explicitly obtains authorization credentials for the charm store
// and stores them in the client's cookie jar. If there was an error
// perfoming a login interaction then the error will have a cause of type
// *httpbakery.InteractionError.
func (cs *Client) Login() error {
	if err := cs.Get("/delegatable-macaroon", &struct{}{}); err != nil {
		return errgo.NoteMask(err, "cannot retrieve the authentication macaroon", isAPIError)
	}
	return nil
}

// WhoAmI returns the user and list of groups associated with the macaroon
// used to authenticate.
func (cs *Client) WhoAmI() (*params.WhoAmIResponse, error) {
	var response params.WhoAmIResponse
	if err := cs.Get("/whoami", &response); err != nil {
		return nil, errgo.Mask(err, isAPIError)
	}
	return &response, nil
}

// CharmRevision holds the revision number of a charm and any error
// encountered in retrieving it.
type CharmRevision struct {
	Revision int
	Err      error
}

// Latest returns the most current revision for each of the identified
// charms. The revision in the provided charm URLs is ignored.
func (cs *Client) Latest(curls []*charm.URL) ([]CharmRevision, error) {
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
		}
	}
	if err := cs.Get(u.String(), &results); err != nil {
		return nil, errgo.NoteMask(err, "cannot get metadata from the charm store", isAPIError)
	}

	// Build the response.
	responses := make([]CharmRevision, len(curls))
	for i, url := range urls {
		result, found := results[url]
		if !found {
			responses[i] = CharmRevision{
				Err: params.ErrNotFound,
			}
			continue
		}
		responses[i] = CharmRevision{
			Revision: result.Meta.IdRevision.Revision,
		}
	}
	return responses, nil
}

// JujuMetadataHTTPHeader is the HTTP header name used to send Juju metadata
// attributes to the charm store.
const JujuMetadataHTTPHeader = "Juju-Metadata"

// IsAuthorizationError reports whether the given error
// was returned because authorization was denied for a
// charmstore request.
func IsAuthorizationError(err error) bool {
	err = errgo.Cause(err)
	switch {
	case httpbakery.IsDischargeError(err):
		return true
	case httpbakery.IsInteractionError(err):
		return true
	case err == params.ErrUnauthorized:
		return true
	}
	return false
}

func isAPIError(err error) bool {
	if err == nil {
		return false
	}
	err = errgo.Cause(err)
	if _, ok := err.(params.ErrorCode); ok {
		return true
	}
	return IsAuthorizationError(err)
}

// noProgress implements Progress by doing nothing.
type noProgress struct{}

func (noProgress) Start(uploadId string, expires time.Time) {}

func (noProgress) Transferred(total int64) {}

func (noProgress) Error(err error) {}

func (noProgress) Finalizing() {}
