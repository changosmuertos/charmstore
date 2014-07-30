// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

// The router package implements an HTTP request router for charm store
// HTTP requests.
package router

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"

	charm "gopkg.in/juju/charm.v2"

	"github.com/juju/charmstore/params"
)

var knownSeries = map[string]bool{
	"bundle":  true,
	"precise": true,
	"quantal": true,
	"raring":  true,
	"saucy":   true,
	"trusty":  true,
	"utopic":  true,
}

// BulkIncludeHandler represents a metadata handler that can
// handle multiple metadata "include" requests in a single batch.
//
// For simple metadata handlers that cannot be
// efficiently combined, see SingleIncludeHandler,
type BulkIncludeHandler interface {
	// Key returns a value that will be used to group handlers
	// together in preparation for a call to Handle.
	// The key should be comparable for equality.
	// Please do not return NaN. That would be silly, OK?
	Key() interface{}

	// Handle returns the results of invoking all the given handlers
	// on the given charm or bundle id. Each result is held in
	// the respective element of the returned slice.
	//
	// All of the handlers' Keys will be equal to the receiving handler's
	// Key.
	//
	// Each item in paths holds the remaining metadata path
	// for the handler in the corresponding position
	// in hs after the prefix in Handlers.Meta has been stripped,
	// and flags holds all the url query values.
	//
	// TODO(rog) document indexed errors.
	Handle(hs []BulkIncludeHandler, id *charm.URL, paths []string, flags url.Values) ([]interface{}, error)
}

// IdHandler handles a charm store request rooted at the given id.
// The request path (req.URL.Path) holds the URL path after
// the id has been stripped off.
type IdHandler func(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error

// Handlers specifies how HTTP requests will be routed
// by the router.
type Handlers struct {
	// Global holds handlers for paths not matched by Meta or Id.
	// The map key is the path; the value is the handler that will
	// be used to handle that path.
	//
	// Path matching is by matched by longest-prefix - the same as
	// http.ServeMux.
	Global map[string]http.Handler

	// Id holds handlers for paths which correspond to a single
	// charm or bundle id other than the meta path. The map key
	// holds the first element of the path, which may end in a
	// trailing slash (/) to indicate that longer paths are allowed
	// too.
	Id map[string]IdHandler

	// Meta holds metadata handlers for paths under the meta
	// endpoint. The map key holds the first element of the path,
	// which may end in a trailing slash (/) to indicate that longer
	// paths are allowed too.
	Meta map[string]BulkIncludeHandler
}

// Router represents a charm store HTTP request router.
type Router struct {
	handlers   *Handlers
	handler    http.Handler
	resolveURL func(url *charm.URL) error
}

// New returns a charm store router that will route requests to
// the given handlers and retrieve metadata from the given database.
//
// The resolveURL function will be called to resolve ids in
// router paths - it should fill in the Series and Revision
// fields of its argument URL if they are not specified.
func New(handlers *Handlers, resolveURL func(url *charm.URL) error) *Router {
	r := &Router{
		handlers:   handlers,
		resolveURL: resolveURL,
	}
	mux := http.NewServeMux()
	mux.Handle("/meta/", http.StripPrefix("/meta", HandleJSON(r.serveBulkMeta)))
	for path, handler := range r.handlers.Global {
		mux.Handle("/"+path, handler)
	}
	mux.Handle("/", HandleErrors(r.serveIds))
	r.handler = mux
	return r
}

var (
	ErrNotFound     = fmt.Errorf("not found")
	ErrDataNotFound = fmt.Errorf("metadata not found")
)

// ServeHTTP implements http.Handler.ServeHTTP.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}

// Handlers returns the set of handlers that the router was created with.
// This should not be changed.
func (r *Router) Handlers() *Handlers {
	return r.handlers
}

// serveIds serves requests that may be rooted at a charm or bundle id.
func (r *Router) serveIds(w http.ResponseWriter, req *http.Request) error {
	if err := req.ParseForm(); err != nil {
		return err
	}
	// We can ignore a trailing / because we do not return any
	// relative URLs. If we start to return relative URL redirects,
	// we will need to redirect non-slash-terminated URLs
	// to slash-terminated URLs.
	// http://cdivilly.wordpress.com/2014/03/11/why-trailing-slashes-on-uris-are-important/
	path := strings.TrimSuffix(req.URL.Path, "/")
	url, path, err := splitId(path)
	if err != nil {
		return err
	}
	if err := r.resolveURL(url); err != nil {
		return err
	}
	key, path := handlerKey(path)
	if key == "" {
		return ErrNotFound
	}
	if handler, ok := r.handlers.Id[key]; ok {
		req.URL.Path = path
		return handler(url, w, req)
	}
	if key != "meta/" && key != "meta" {
		return ErrNotFound
	}
	req.URL.Path = path
	resp, err := r.serveMeta(url, req)
	if err != nil {
		return err
	}
	WriteJSON(w, http.StatusOK, resp)
	return nil
}

// handlerKey returns a key that can be used to look up a handler at the
// given path, and the remaining path elements. If there is no possible
// key, the returned key is empty.
func handlerKey(path string) (key, rest string) {
	path = strings.TrimPrefix(path, "/")
	key, i := splitPath(path, 0)
	if key == "" {
		// TODO what *should* we get if we GET just an id?
		return "", rest
	}
	if i < len(path)-1 {
		// There are more elements, so include the / character
		// that terminates the element.
		return path[0 : i+1], path[i:]
	}
	return key, ""
}

func (r *Router) serveMeta(id *charm.URL, req *http.Request) (interface{}, error) {
	key, path := handlerKey(req.URL.Path)
	if key == "" {
		// GET id/meta
		// http://tinyurl.com/nysdjly
		return r.metaNames(), nil
	}
	if key == "any" {
		// GET id/meta/any?[include=meta[&include=meta...]]
		// http://tinyurl.com/q5vcjpk
		meta, err := r.GetMetadata(id, req.Form["include"])
		if err != nil {
			return nil, err
		}
		return params.MetaAnyResponse{
			Id:   id,
			Meta: meta,
		}, nil
	}
	if handler := r.handlers.Meta[key]; handler != nil {
		results, err := handler.Handle([]BulkIncludeHandler{handler}, id, []string{path}, req.Form)
		if err != nil {
			return nil, err
		}
		result := results[0]
		if isNull(result) {
			return nil, ErrDataNotFound
		}
		return results[0], nil
	}
	return nil, ErrNotFound
}

// isNull reports whether the given value will encode to
// a null JSON value.
func isNull(val interface{}) bool {
	if val == nil {
		return true
	}
	v := reflect.ValueOf(val)
	if kind := v.Kind(); kind != reflect.Map && kind != reflect.Ptr && kind != reflect.Slice {
		return false
	}
	return v.IsNil()
}

func (r *Router) metaNames() []string {
	names := make([]string, 0, len(r.handlers.Meta))
	for name := range r.handlers.Meta {
		names = append(names, strings.TrimSuffix(name, "/"))
	}
	sort.Strings(names)
	return names
}

// serveBulkMeta serves the "bulk" metadata retrieval endpoint
// that can return information on several ids at once.
//
// GET meta/$endpoint?id=$id0[&id=$id1...][$otherflags]
// http://tinyurl.com/kdrly9f
func (r *Router) serveBulkMeta(w http.ResponseWriter, req *http.Request) (interface{}, error) {
	// TODO get the metadata concurrently for each id.
	req.ParseForm()
	ids := req.Form["id"]
	if len(ids) == 0 {
		return nil, fmt.Errorf("no ids specified in meta request")
	}
	delete(req.Form, "id")
	result := make(map[string]interface{})
	for _, id := range ids {
		url, err := parseURL(id)
		if err != nil {
			return nil, err
		}
		if err := r.resolveURL(url); err != nil {
			if err == ErrNotFound {
				// URLs not found will be omitted from the result.
				// http://tinyurl.com/o5ptfkk
				continue
			}
			return nil, err
		}
		meta, err := r.serveMeta(url, req)
		if err == ErrDataNotFound {
			// The relevant data does not exist.
			// http://tinyurl.com/o5ptfkk
			continue
		}
		if err != nil {
			return nil, err
		}
		result[id] = meta
	}
	return result, nil
}

// GetMetadata retrieves metadata for the given charm or bundle id,
// including information as specified by the includes slice.
func (r *Router) GetMetadata(id *charm.URL, includes []string) (map[string]interface{}, error) {
	groups := make(map[interface{}][]BulkIncludeHandler)
	includesByGroup := make(map[interface{}][]string)
	for _, include := range includes {
		// Get the key that lets us choose the include handler.
		includeKey, _ := handlerKey(include)
		handler := r.handlers.Meta[includeKey]
		if handler == nil {
			return nil, fmt.Errorf("unrecognized metadata name %q", include)
		}

		// Get the key that lets us group this handler into the
		// correct bulk group.
		key := handler.Key()
		groups[key] = append(groups[key], handler)
		includesByGroup[key] = append(includesByGroup[key], include)
	}
	results := make(map[string]interface{})
	for _, g := range groups {
		// We know that we must have at least one element in the
		// slice here. We could use any member of the slice to
		// actually handle the request, so arbitrarily choose
		// g[0]. Note that g[0].Key() is equal to g[i].Key() for
		// every i in the slice.
		groupIncludes := includesByGroup[g[0].Key()]

		// Paths contains all the path elements after
		// the handler key has been stripped off.
		paths := make([]string, len(g))
		for i, include := range groupIncludes {
			_, paths[i] = handlerKey(include)
		}
		groupResults, err := g[0].Handle(g, id, paths, nil)
		if err != nil {
			// TODO(rog) if it's a BulkError, attach
			// the original include path to error (the BulkError
			// should contain the index of the failed one).
			return nil, err
		}
		for i, result := range groupResults {
			// Omit nil results from map. Note: omit statically typed
			// nil results too to make it easy for handlers to return
			// possibly nil data with a static type.
			// http://tinyurl.com/o5ptfkk
			if !isNull(result) {
				results[groupIncludes[i]] = result
			}
		}
	}
	return results, nil
}

// splitPath returns the first path element
// after path[i:] and the start of the next
// element.
//
// For example, splitPath("/foo/bar/bzr", 4) returns ("bar", 8).
func splitPath(path string, i int) (elem string, nextIndex int) {
	if i < len(path) && path[i] == '/' {
		i++
	}
	j := strings.Index(path[i:], "/")
	if j == -1 {
		return path[i:], len(path)
	}
	j += i
	return path[i:j], j
}

func splitId(path string) (url *charm.URL, rest string, err error) {
	path = strings.TrimPrefix(path, "/")

	part, i := splitPath(path, 0)

	// skip ~<username>
	if strings.HasPrefix(part, "~") {
		part, i = splitPath(path, i)
	}
	// skip series
	if knownSeries[part] {
		part, i = splitPath(path, i)
	}

	// part should now contain the charm name,
	// and path[0:i] should contain the entire
	// charm id.

	urlStr := strings.TrimSuffix(path[0:i], "/")
	url, err = parseURL(urlStr)
	if err != nil {
		return nil, "", err
	}
	return url, path[i:], nil
}

func parseURL(urlStr string) (*charm.URL, error) {
	ref, series, err := charm.ParseReference(urlStr)
	if err != nil {
		return nil, err
	}
	return &charm.URL{
		Reference: ref,
		Series:    series,
	}, nil
}