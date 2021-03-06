// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/juju/loggo"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6"
	"gopkg.in/juju/charmrepo.v3/csclient/params"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/juju/charmstore.v5/internal/router"
	"gopkg.in/juju/charmstore.v5/internal/storetesting"
)

type SearchSuite struct {
	commonSuite
}

var _ = gc.Suite(&SearchSuite{})

var exportTestCharms = map[string]*router.ResolvedURL{
	"multi-series": newResolvedURL("cs:~charmers/multi-series-0", 0),
	"wordpress":    newResolvedURL("cs:~charmers/precise/wordpress-23", 23),
	"mysql":        newResolvedURL("cs:~openstack-charmers/trusty/mysql-7", 7),
	"varnish":      newResolvedURL("cs:~foo/trusty/varnish-1", -1),
	// Note: the riak charm is set up without read-everyone permissions.
	"riak": newResolvedURL("cs:~charmers/trusty/riak-67", 67),
}

var exportTestBundles = map[string]*router.ResolvedURL{
	"wordpress-simple": newResolvedURL("cs:~charmers/bundle/wordpress-simple-4", 4),
}

func (s *SearchSuite) SetUpSuite(c *gc.C) {
	s.enableES = true
	s.enableIdentity = true
	s.commonSuite.SetUpSuite(c)
}

func (s *SearchSuite) SetUpTest(c *gc.C) {
	s.commonSuite.SetUpTest(c)
	s.addCharmsToStore(c)
	err := s.store.SetPerms(charm.MustParseURL("cs:~charmers/riak"), "stable.read", "charmers", "test-user")
	c.Assert(err, gc.Equals, nil)
	err = s.store.UpdateSearch(newResolvedURL("~charmers/trusty/riak-0", 0))
	c.Assert(err, gc.Equals, nil)
	err = s.esSuite.ES.RefreshIndex(s.esSuite.TestIndex)
	c.Assert(err, gc.Equals, nil)
}

func (s *SearchSuite) addCharmsToStore(c *gc.C) {
	for name, id := range exportTestCharms {
		s.addPublicCharm(c, getSearchCharm(name), id)
	}
	for name, id := range exportTestBundles {
		s.addPublicBundle(c, getSearchBundle(name), id, false)
	}
}

func getSearchCharm(name string) *storetesting.Charm {
	ca := storetesting.Charms.CharmDir(name)
	meta := ca.Meta()
	meta.Categories = append(strings.Split(name, "-"), "bar")
	return storetesting.NewCharm(meta)
}

func getSearchBundle(name string) *storetesting.Bundle {
	ba := storetesting.Charms.BundleDir(name)
	data := ba.Data()
	data.Tags = append(strings.Split(name, "-"), "baz")
	return storetesting.NewBundle(data)
}

func (s *SearchSuite) TestSuccessfulSearches(c *gc.C) {
	tests := []struct {
		about   string
		query   string
		results []*router.ResolvedURL
	}{{
		about: "bare search",
		query: "",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "text search",
		query: "text=wordpress",
		results: []*router.ResolvedURL{
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "autocomplete search",
		query: "text=word&autocomplete=1",
		results: []*router.ResolvedURL{
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "blank text search",
		query: "text=",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "description filter search",
		query: "description=database",
		results: []*router.ResolvedURL{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "name filter search",
		query: "name=mysql",
		results: []*router.ResolvedURL{
			exportTestCharms["mysql"],
		},
	}, {
		about: "owner filter search",
		query: "owner=foo",
		results: []*router.ResolvedURL{
			exportTestCharms["varnish"],
		},
	}, {
		about: "provides filter search",
		query: "provides=mysql",
		results: []*router.ResolvedURL{
			exportTestCharms["mysql"],
		},
	}, {
		about: "requires filter search",
		query: "requires=mysql",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			exportTestCharms["wordpress"],
		},
	}, {
		about: "series filter search",
		query: "series=trusty",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "summary filter search",
		query: "summary=database",
		results: []*router.ResolvedURL{
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
		},
	}, {
		about: "tags filter search",
		query: "tags=wordpress",
		results: []*router.ResolvedURL{
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "type filter search",
		query: "type=bundle",
		results: []*router.ResolvedURL{
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "multiple type filter search",
		query: "type=bundle&type=charm",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "provides multiple interfaces filter search",
		query: "provides=monitoring+http",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			exportTestCharms["wordpress"],
		},
	}, {
		about: "requires multiple interfaces filter search",
		query: "requires=mysql+varnish",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			exportTestCharms["wordpress"],
		},
	}, {
		about: "multiple tags filter search",
		query: "tags=mysql+bar",
		results: []*router.ResolvedURL{
			exportTestCharms["mysql"],
		},
	}, {
		about: "blank owner",
		query: "owner=",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "paginated search",
		query: "name=mysql&skip=1",
	}, {
		about: "promulgated",
		query: "promulgated=1",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			exportTestCharms["wordpress"],
			exportTestCharms["mysql"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "not promulgated",
		query: "promulgated=0",
		results: []*router.ResolvedURL{
			exportTestCharms["varnish"],
		},
	}, {
		about: "promulgated with owner",
		query: "promulgated=1&owner=openstack-charmers",
		results: []*router.ResolvedURL{
			exportTestCharms["mysql"],
		},
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.about)
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL("search?" + test.query),
		})
		var sr params.SearchResponse
		err := json.Unmarshal(rec.Body.Bytes(), &sr)
		c.Assert(err, gc.Equals, nil)
		c.Assert(sr.Results, gc.HasLen, len(test.results))
		c.Logf("results: %s", rec.Body.Bytes())
		assertResultSet(c, sr, test.results)
	}
}

func (s *SearchSuite) TestPaginatedSearch(c *gc.C) {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search?text=wordpress&skip=1"),
	})
	var sr params.SearchResponse
	err := json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.Equals, nil)
	c.Assert(sr.Results, gc.HasLen, 1)
	c.Assert(sr.Total, gc.Equals, 2)
}

func (s *SearchSuite) TestMetadataFields(c *gc.C) {
	tests := []struct {
		about string
		query string
		meta  map[string]interface{}
	}{{
		about: "archive-size",
		query: "name=mysql&include=archive-size",
		meta: map[string]interface{}{
			"archive-size": params.ArchiveSizeResponse{getSearchCharm("mysql").Size()},
		},
	}, {
		about: "bundle-metadata",
		query: "name=wordpress-simple&type=bundle&include=bundle-metadata",
		meta: map[string]interface{}{
			"bundle-metadata": v4BundleMetadata(getSearchBundle("wordpress-simple").Data()), // V4 SPECIFIC
		},
	}, {
		about: "bundle-machine-count",
		query: "name=wordpress-simple&type=bundle&include=bundle-machine-count",
		meta: map[string]interface{}{
			"bundle-machine-count": params.BundleCount{2},
		},
	}, {
		about: "bundle-unit-count",
		query: "name=wordpress-simple&type=bundle&include=bundle-unit-count",
		meta: map[string]interface{}{
			"bundle-unit-count": params.BundleCount{2},
		},
	}, {
		about: "charm-actions",
		query: "name=wordpress&type=charm&include=charm-actions",
		meta: map[string]interface{}{
			"charm-actions": getSearchCharm("wordpress").Actions(),
		},
	}, {
		about: "charm-config",
		query: "name=wordpress&type=charm&include=charm-config",
		meta: map[string]interface{}{
			"charm-config": getSearchCharm("wordpress").Config(),
		},
	}, {
		about: "charm-related",
		query: "name=wordpress&type=charm&include=charm-related",
		meta: map[string]interface{}{
			"charm-related": params.RelatedResponse{
				Provides: map[string][]params.EntityResult{
					"mysql": {
						{
							Id: exportTestCharms["mysql"].PreferredURL(),
						},
					},
					"varnish": {
						{
							Id: exportTestCharms["varnish"].PreferredURL(),
						},
					},
				},
			},
		},
	}, {
		about: "multiple values",
		query: "name=wordpress&type=charm&include=charm-related&include=charm-config",
		meta: map[string]interface{}{
			"charm-related": params.RelatedResponse{
				Provides: map[string][]params.EntityResult{
					"mysql": {
						{
							Id: exportTestCharms["mysql"].PreferredURL(),
						},
					},
					"varnish": {
						{
							Id: exportTestCharms["varnish"].PreferredURL(),
						},
					},
				},
			},
			"charm-config": getSearchCharm("wordpress").Config(),
		},
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.about)
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL("search?" + test.query),
		})
		c.Assert(rec.Code, gc.Equals, http.StatusOK)
		var sr struct {
			Results []struct {
				Meta json.RawMessage
			}
		}
		err := json.Unmarshal(rec.Body.Bytes(), &sr)
		c.Assert(err, gc.Equals, nil)
		c.Assert(sr.Results, gc.HasLen, 1)
		c.Assert(string(sr.Results[0].Meta), jc.JSONEquals, test.meta)
	}
}

func (s *SearchSuite) TestSearchError(c *gc.C) {
	err := s.esSuite.ES.DeleteIndex(s.esSuite.TestIndex)
	c.Assert(err, gc.Equals, nil)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search?name=wordpress"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusInternalServerError)
	var resp params.Error
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	c.Assert(err, gc.Equals, nil)
	c.Assert(resp.Code, gc.Equals, params.ErrorCode(""))
	c.Assert(resp.Message, gc.Matches, "error performing search: search failed: .*")
}

func (s *SearchSuite) TestSearchIncludeError(c *gc.C) {
	// Perform a search for all charms, including the
	// manifest, which will try to retrieve all charm
	// blobs.
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search?type=charm&include=manifest"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	var resp params.SearchResponse
	err := json.Unmarshal(rec.Body.Bytes(), &resp)
	// V4 SPECIFIC
	// cs:riak will not be found because it is not visible to "everyone".
	// cs:multi-series will be expanded to 4 different results.
	c.Assert(resp.Results, gc.HasLen, len(exportTestCharms)+3-1)

	// Now update the entity to hold an invalid hash.
	// The list should still work, but only return a single result.
	err = s.store.UpdateEntity(newResolvedURL("~charmers/precise/wordpress-23", 23), bson.D{{
		"$set", bson.D{{
			"blobhash", hashOfString("nope"),
		}},
	}})
	c.Assert(err, gc.Equals, nil)

	// Now search again - we should get one result less
	// (and the error will be logged).

	// Register a logger that so that we can check the logging output.
	// It will be automatically removed later because IsolatedMgoESSuite
	// uses LoggingSuite.
	var tw loggo.TestWriter
	err = loggo.RegisterWriter("test-log", &tw)
	c.Assert(err, gc.Equals, nil)

	rec = httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search?type=charm&include=manifest"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	resp = params.SearchResponse{}
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	// V4 SPECIFIC
	// cs:riak will not be found because it is not visible to "everyone".
	// cs:multi-series will be expanded to 4 different results.
	// cs:wordpress will not be found because it has no manifest.
	c.Assert(resp.Results, gc.HasLen, len(exportTestCharms)+3-2)

	c.Assert(tw.Log(), jc.LogMatches, []string{"cannot retrieve metadata for cs:precise/wordpress-23: cannot open archive data for cs:precise/wordpress-23: .*"})
}

func (s *SearchSuite) TestSorting(c *gc.C) {
	tests := []struct {
		about   string
		query   string
		results []*router.ResolvedURL
	}{{
		about: "name ascending",
		query: "sort=name",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "name descending",
		query: "sort=-name",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			exportTestBundles["wordpress-simple"],
			exportTestCharms["wordpress"],
			exportTestCharms["varnish"],
			exportTestCharms["mysql"],
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
		},
	}, {
		about: "series ascending",
		query: "sort=series,name",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			exportTestBundles["wordpress-simple"],
			exportTestCharms["wordpress"],
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
		},
	}, {
		about: "series descending",
		query: "sort=-series&sort=name",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}, {
		about: "owner ascending",
		query: "sort=owner,name",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
			exportTestCharms["varnish"],
			exportTestCharms["mysql"],
		},
	}, {
		about: "owner descending",
		query: "sort=-owner&sort=name",
		results: []*router.ResolvedURL{
			// V4 SPECIFIC
			exportTestCharms["mysql"],
			exportTestCharms["varnish"],
			router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
			router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
			exportTestCharms["wordpress"],
			exportTestBundles["wordpress-simple"],
		},
	}}
	for i, test := range tests {
		c.Logf("test %d. %s", i, test.about)
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL("search?" + test.query),
		})
		var sr params.SearchResponse
		err := json.Unmarshal(rec.Body.Bytes(), &sr)
		c.Assert(err, gc.Equals, nil)
		// Not using assertResultSet(c, sr, test.results) as it does sort internally
		c.Assert(sr.Results, gc.HasLen, len(test.results), gc.Commentf("expected %#v", test.results))
		c.Logf("results: %s", rec.Body.Bytes())
		for i := range test.results {
			c.Assert(sr.Results[i].Id.String(), gc.Equals, test.results[i].PreferredURL().String(), gc.Commentf("element %d"))
		}
	}
}

func (s *SearchSuite) TestSortUnsupportedField(c *gc.C) {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search?sort=foo"),
	})
	var e params.Error
	err := json.Unmarshal(rec.Body.Bytes(), &e)
	c.Assert(err, gc.Equals, nil)
	c.Assert(e.Code, gc.Equals, params.ErrBadRequest)
	c.Assert(e.Message, gc.Equals, "invalid sort field: unrecognized sort parameter \"foo\"")
}

func (s *SearchSuite) TestDownloadsBoost(c *gc.C) {
	charmDownloads := map[string]int{
		"mysql":     0,
		"wordpress": 1,
		"varnish":   8,
	}
	for n, cnt := range charmDownloads {
		url := newResolvedURL("cs:~downloads-test/trusty/x-1", -1)
		url.URL.Name = n
		s.addPublicCharm(c, getSearchCharm(n), url)
		for i := 0; i < cnt; i++ {
			err := s.store.IncrementDownloadCounts(url)
			c.Assert(err, gc.Equals, nil)
		}
	}
	err := s.esSuite.ES.RefreshIndex(s.esSuite.TestIndex)
	c.Assert(err, gc.Equals, nil)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search?owner=downloads-test"),
	})
	var sr params.SearchResponse
	err = json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.Equals, nil)
	c.Assert(sr.Results, gc.HasLen, 3)
	c.Assert(sr.Results[0].Id.Name, gc.Equals, "varnish")
	c.Assert(sr.Results[1].Id.Name, gc.Equals, "wordpress")
	c.Assert(sr.Results[2].Id.Name, gc.Equals, "mysql")
}

func (s *SearchSuite) TestSearchWithAdminCredentials(c *gc.C) {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler:  s.srv,
		URL:      storeURL("search"),
		Username: testUsername,
		Password: testPassword,
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	expected := []*router.ResolvedURL{
		// V4 SPECIFIC
		router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
		exportTestCharms["mysql"],
		exportTestCharms["wordpress"],
		exportTestCharms["riak"],
		exportTestCharms["varnish"],
		exportTestBundles["wordpress-simple"],
	}
	var sr params.SearchResponse
	err := json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.Equals, nil)
	assertResultSet(c, sr, expected)
}

func (s *SearchSuite) TestSearchWithUserMacaroon(c *gc.C) {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search"),
		Do:      bakeryDo(s.login("test-user")),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	expected := []*router.ResolvedURL{
		// V4 SPECIFIC
		router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
		exportTestCharms["mysql"],
		exportTestCharms["wordpress"],
		exportTestCharms["riak"],
		exportTestCharms["varnish"],
		exportTestBundles["wordpress-simple"],
	}
	var sr params.SearchResponse
	err := json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.Equals, nil)
	assertResultSet(c, sr, expected)
}

func (s *SearchSuite) TestSearchWithUserInGroups(c *gc.C) {
	s.idmServer.AddUser("bob", "test-user", "test-user2")
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("search"),
		Do:      bakeryDo(s.login("bob")),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	expected := []*router.ResolvedURL{
		// V4 SPECIFIC
		router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
		exportTestCharms["mysql"],
		exportTestCharms["wordpress"],
		exportTestCharms["riak"],
		exportTestCharms["varnish"],
		exportTestBundles["wordpress-simple"],
	}
	var sr params.SearchResponse
	err := json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.Equals, nil)
	assertResultSet(c, sr, expected)
}

func (s *SearchSuite) TestSearchWithBadAdminCredentialsAndACookie(c *gc.C) {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler:  s.srv,
		Do:       s.bakeryDoAsUser("test-user"),
		URL:      storeURL("search"),
		Username: testUsername,
		Password: "bad-password",
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	expected := []*router.ResolvedURL{
		// V4 SPECIFIC
		router.MustNewResolvedURL("cs:~charmers/trusty/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/utopic/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/vivid/multi-series-0", 0),
		router.MustNewResolvedURL("cs:~charmers/wily/multi-series-0", 0),
		exportTestCharms["mysql"],
		exportTestCharms["wordpress"],
		exportTestCharms["varnish"],
		exportTestBundles["wordpress-simple"],
	}
	var sr params.SearchResponse
	err := json.Unmarshal(rec.Body.Bytes(), &sr)
	c.Assert(err, gc.Equals, nil)
	assertResultSet(c, sr, expected)
}

func assertResultSet(c *gc.C, sr params.SearchResponse, expected []*router.ResolvedURL) {
	results := make([]string, len(sr.Results))
	for i, r := range sr.Results {
		results[i] = r.Id.String()
	}
	expect := make([]string, len(expected))
	for i, e := range expected {
		expect[i] = e.PreferredURL().String()
	}
	sort.Strings(results)
	sort.Strings(expect)
	c.Assert(results, jc.DeepEquals, expect)
}

type searchResultById []params.EntityResult

func (s searchResultById) Len() int      { return len(s) }
func (s searchResultById) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s searchResultById) Less(i, j int) bool {
	return s[i].Id.String() < s[j].Id.String()
}

type resolvedURLByPreferredURL []*router.ResolvedURL

func (s resolvedURLByPreferredURL) Len() int      { return len(s) }
func (s resolvedURLByPreferredURL) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s resolvedURLByPreferredURL) Less(i, j int) bool {
	return s[i].PreferredURL().String() < s[j].PreferredURL().String()
}
