// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"

	jc "github.com/juju/testing/checkers"
	"gopkg.in/juju/charm.v2"
	"gopkg.in/juju/charm.v2/testing"
	"gopkg.in/mgo.v2"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/blobstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/params"
)

type StoreSuite struct {
	storetesting.IsolatedMgoSuite
}

var _ = gc.Suite(&StoreSuite{})

func (s *StoreSuite) checkAddCharm(c *gc.C, ch charm.Charm) {
	store := NewStore(s.Session.DB("foo"))
	url := charm.MustParseURL("cs:precise/wordpress-23")
	err := store.AddCharm(url, ch)
	c.Assert(err, gc.IsNil)

	var doc mongodoc.Entity
	err = store.DB.Entities().FindId("cs:precise/wordpress-23").One(&doc)
	c.Assert(err, gc.IsNil)

	// The entity doc has been correctly added to the mongo collection.
	size, hash := mustGetSizeAndHash(ch)
	sort.Strings(doc.CharmProvidedInterfaces)
	sort.Strings(doc.CharmRequiredInterfaces)
	c.Assert(doc, jc.DeepEquals, mongodoc.Entity{
		URL:                     (*params.CharmURL)(url),
		BaseURL:                 mustParseURL("cs:wordpress"),
		BlobHash:                hash,
		Size:                    size,
		CharmMeta:               ch.Meta(),
		CharmActions:            ch.Actions(),
		CharmConfig:             ch.Config(),
		CharmProvidedInterfaces: []string{"http", "logging", "monitoring"},
		CharmRequiredInterfaces: []string{"mysql", "varnish"},
	})

	// The charm archive has been properly added to the blob store.
	r, obtainedSize, err := store.BlobStore.Open(hash)
	c.Assert(err, gc.IsNil)
	c.Assert(obtainedSize, gc.Equals, size)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	charmArchive, err := charm.ReadCharmArchiveBytes(data)
	c.Assert(err, gc.IsNil)
	c.Assert(charmArchive.Meta(), jc.DeepEquals, ch.Meta())
	c.Assert(charmArchive.Config(), jc.DeepEquals, ch.Config())
	c.Assert(charmArchive.Actions(), jc.DeepEquals, ch.Actions())
	c.Assert(charmArchive.Revision(), jc.DeepEquals, ch.Revision())

	// Try inserting the charm again - it should fail because the charm is
	// already there.
	err = store.AddCharm(url, ch)
	c.Assert(err, jc.Satisfies, mgo.IsDup)
}

func (s *StoreSuite) checkAddBundle(c *gc.C, bundle charm.Bundle) {
	store := NewStore(s.Session.DB("foo"))
	url := charm.MustParseURL("cs:bundle/wordpress-simple-42")
	err := store.AddBundle(url, bundle)
	c.Assert(err, gc.IsNil)

	var doc mongodoc.Entity
	err = store.DB.Entities().FindId("cs:bundle/wordpress-simple-42").One(&doc)
	c.Assert(err, gc.IsNil)
	sort.Sort(orderedURLs(doc.BundleCharms))

	// The entity doc has been correctly added to the mongo collection.
	size, hash := mustGetSizeAndHash(bundle)
	c.Assert(doc, jc.DeepEquals, mongodoc.Entity{
		URL:          (*params.CharmURL)(url),
		BaseURL:      mustParseURL("cs:wordpress-simple"),
		BlobHash:     hash,
		Size:         size,
		BundleData:   bundle.Data(),
		BundleReadMe: bundle.ReadMe(),
		BundleCharms: []*params.CharmURL{
			(*params.CharmURL)(mustParseURL("mysql")),
			(*params.CharmURL)(mustParseURL("wordpress")),
		},
	})

	// The bundle archive has been properly added to the blob store.
	r, obtainedSize, err := store.BlobStore.Open(hash)
	c.Assert(err, gc.IsNil)
	c.Assert(obtainedSize, gc.Equals, size)
	data, err := ioutil.ReadAll(r)
	c.Assert(err, gc.IsNil)
	bundleArchive, err := charm.ReadBundleArchiveBytes(data, verifyConstraints)
	c.Assert(err, gc.IsNil)
	c.Assert(bundleArchive.Data(), jc.DeepEquals, bundle.Data())
	c.Assert(bundleArchive.ReadMe(), jc.DeepEquals, bundle.ReadMe())

	// Try inserting the bundle again - it should fail because the bundle is
	// already there.
	err = store.AddBundle(url, bundle)
	c.Assert(err, jc.Satisfies, mgo.IsDup)
}

type orderedURLs []*params.CharmURL

func (o orderedURLs) Less(i, j int) bool {
	return o[i].String() < o[j].String()
}

func (o orderedURLs) Swap(i, j int) {
	o[i], o[j] = o[j], o[i]
}

func (o orderedURLs) Len() int {
	return len(o)
}

var expandURLTests = []struct {
	inStore []string
	expand  string
	expect  []string
}{{
	inStore: []string{"cs:precise/wordpress-23"},
	expand:  "wordpress",
	expect:  []string{"cs:precise/wordpress-23"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:precise/wordpress-24"},
	expand:  "wordpress",
	expect:  []string{"cs:precise/wordpress-23", "cs:precise/wordpress-24"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-24"},
	expand:  "precise/wordpress",
	expect:  []string{"cs:precise/wordpress-23"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-24", "cs:foo/bar-434"},
	expand:  "wordpress",
	expect:  []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-24"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-23", "cs:trusty/wordpress-24"},
	expand:  "wordpress-23",
	expect:  []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-23"},
}, {
	inStore: []string{"cs:~user/precise/wordpress-23", "cs:~user/trusty/wordpress-23"},
	expand:  "~user/precise/wordpress",
	expect:  []string{"cs:~user/precise/wordpress-23"},
}, {
	inStore: []string{"cs:~user/precise/wordpress-23", "cs:~user/trusty/wordpress-23"},
	expand:  "~user/wordpress",
	expect:  []string{"cs:~user/precise/wordpress-23", "cs:~user/trusty/wordpress-23"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-24", "cs:foo/bar-434"},
	expand:  "precise/wordpress-23",
	expect:  []string{"cs:precise/wordpress-23"},
}, {
	inStore: []string{"cs:precise/wordpress-23", "cs:trusty/wordpress-24", "cs:foo/bar-434"},
	expand:  "arble",
	expect:  []string{},
}}

func (s *StoreSuite) TestExpandURL(c *gc.C) {
	wordpress := testing.Charms.CharmDir("wordpress")
	for i, test := range expandURLTests {
		c.Logf("test %d: %q from %q", i, test.expand, test.inStore)
		store := NewStore(s.Session.DB("foo"))
		_, err := store.DB.Entities().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
		urls := mustParseURLs(test.inStore)
		for _, url := range urls {
			err := store.AddCharm(url, wordpress)
			c.Assert(err, gc.IsNil)
		}
		gotURLs, err := store.ExpandURL((*charm.URL)(mustParseURL(test.expand)))
		c.Assert(err, gc.IsNil)

		gotURLStrs := urlStrings(gotURLs)
		sort.Strings(gotURLStrs)
		c.Assert(gotURLStrs, jc.DeepEquals, test.expect)
	}
}

func urlStrings(urls []*charm.URL) []string {
	urlStrs := make([]string, len(urls))
	for i, url := range urls {
		urlStrs[i] = url.String()
	}
	return urlStrs
}

func mustParseURLs(urlStrs []string) []*charm.URL {
	urls := make([]*charm.URL, len(urlStrs))
	for i, u := range urlStrs {
		var err error
		urls[i], err = charm.ParseURL(u)
		if err != nil {
			panic(err)
		}
	}
	return urls
}

func (s *StoreSuite) TestAddCharmDir(c *gc.C) {
	charmDir := testing.Charms.CharmDir("wordpress")
	s.checkAddCharm(c, charmDir)
}

func (s *StoreSuite) TestAddCharmArchive(c *gc.C) {
	charmArchive := testing.Charms.CharmArchive(c.MkDir(), "wordpress")
	s.checkAddCharm(c, charmArchive)
}

func (s *StoreSuite) TestAddBundleDir(c *gc.C) {
	bundleDir := testing.Charms.BundleDir("wordpress")
	s.checkAddBundle(c, bundleDir)
}

func (s *StoreSuite) TestAddBundleArchive(c *gc.C) {
	bundleArchive, err := charm.ReadBundleArchive(
		testing.Charms.BundleArchivePath(c.MkDir(), "wordpress"),
		verifyConstraints)
	c.Assert(err, gc.IsNil)
	s.checkAddBundle(c, bundleArchive)
}

// mustParseURL is like charm.MustParseURL except
// that it allows an unspecified series.
func mustParseURL(urlStr string) *params.CharmURL {
	ref, series, err := charm.ParseReference(urlStr)
	if err != nil {
		panic(err)
	}
	return &params.CharmURL{
		Reference: ref,
		Series:    series,
	}
}

func mustGetSizeAndHash(c interface{}) (int64, string) {
	var r io.ReadWriter
	var err error
	switch c := c.(type) {
	case archiverTo:
		r = new(bytes.Buffer)
		err = c.ArchiveTo(r)
	case *charm.BundleArchive:
		r, err = os.Open(c.Path)
	case *charm.CharmArchive:
		r, err = os.Open(c.Path)
	default:
		panic(fmt.Sprintf("unable to get size and hash for type %T", c))
	}
	if err != nil {
		panic(err)
	}
	hash := blobstore.NewHash()
	size, err := io.Copy(hash, r)
	if err != nil {
		panic(err)
	}
	return size, fmt.Sprintf("%x", hash.Sum(nil))
}

func verifyConstraints(c string) error { return nil }