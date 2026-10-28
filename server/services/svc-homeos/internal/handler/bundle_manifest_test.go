// bundle_manifest_test.go pins the two contract endpoints PRD 17.8 builds 按需加载 on:
// `GET /app/bundles` and `GET /app/version`. Both must answer from one embedded manifest that
// scripts/build-base.sh generates at release time, and both must fail closed (503) rather than
// invent a version when that manifest is unusable.
//
// The assertions are deliberately about the wire shape, not the Go structs: the shell consumes
// JSON keys (`current_version`/`update_notes`/`min_compat`) and the base package is reached from
// `/app/version`, never as a bundle entry. A test built on the struct would keep passing after a
// json tag drift.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// callAppHandler runs one of the two app handlers against a query string and returns the recorder.
func callAppHandler(t *testing.T, path, query string, h gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path+"?"+query, nil)
	h(c)
	return w
}

// TestEmbeddedBundleManifestAgreesWithRegistry is the drift alarm at rest: the committed
// bundle_manifest.json must describe exactly the bundles the registry says are born now, all
// carried by the base package (PRD 17.8 P1 row: 财务分包随基座一起发布).
func TestEmbeddedBundleManifestAgreesWithRegistry(t *testing.T) {
	require.NoError(t, appManifestErr, "embedded bundle_manifest.json must validate")

	assert.Equal(t, registry.BundleSet(), manifestCodes(appManifest),
		"manifest bundles must equal registry.BundleSet() in registry order")

	assert.Regexp(t, semverPattern, appManifest.Base.Version)
	assert.Regexp(t, sha256Pattern, appManifest.Base.SHA256)
	assert.Positive(t, appManifest.Base.Size)
	assert.NotEmpty(t, appManifest.Base.UpdateNotes)

	for _, b := range appManifest.Bundles {
		assert.Equal(t, appManifest.Base.URL, b.URL,
			"P1: bundle %q ships inside the base package", b.Code)
		assert.Equal(t, appManifest.Base.SHA256, b.SHA256)
		assert.Equal(t, appManifest.Base.Size, b.Size)
	}
}

func manifestCodes(m bundleManifest) []string {
	codes := make([]string, 0, len(m.Bundles))
	for _, b := range m.Bundles {
		codes = append(codes, b.Code)
	}
	return codes
}

// TestGetAppBundlesWireShape covers codes filtering, the ignored `installed` snapshot argument,
// and the absence of a "base" entry. Unknown and unborn codes are ignored on purpose: availability
// is decided by /faces and /family/modules, and an unknown string from a stale client must not turn
// the manifest call into a 4xx that blocks on-demand loading.
func TestGetAppBundlesWireShape(t *testing.T) {
	require.NoError(t, appManifestErr)

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{name: "no codes returns every born bundle", query: "", want: registry.BundleSet()},
		{name: "single code", query: "codes=finance", want: []string{"finance"}},
		{name: "spaces and unborn codes are tolerated", query: "codes=+finance+,+purchase+", want: []string{"finance"}},
		{name: "base is not a bundle", query: "codes=homeos,base", want: []string{}},
		{name: "unknown code returns empty", query: "codes=does-not-exist", want: []string{}},
		{name: "installed is accepted and not interpreted", query: `installed={"finance":"0.9.0"}`, want: registry.BundleSet()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := callAppHandler(t, "/api/homeos/app/bundles", tt.query, GetAppBundles)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())

			var body struct {
				Bundles []map[string]any `json:"bundles"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

			got := make([]string, 0, len(body.Bundles))
			for _, b := range body.Bundles {
				code, _ := b["code"].(string)
				got = append(got, code)
				for _, key := range []string{"version", "url", "sha256", "min_compat"} {
					assert.NotEmpty(t, b[key], "%q must be present for bundle %q", key, code)
				}
				assert.Positive(t, b["size"], "size must be positive for bundle %q", code)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestGetAppVersionWireShape pins PRD 3.4.8's field set exactly. The response must carry no family
// data and no invented forced-update flag: ㉕ makes min_compat the only trigger, so the client
// derives it from the same value the bundle manifest carries.
func TestGetAppVersionWireShape(t *testing.T) {
	require.NoError(t, appManifestErr)

	w := callAppHandler(t, "/api/homeos/app/version", "", GetAppVersion)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	assert.ElementsMatch(t, []string{"current_version", "update_notes", "min_compat"}, mapKeys(body),
		"the version response must contain exactly the three PRD fields")
	assert.Equal(t, appManifest.Base.Version, body["current_version"])
	assert.Equal(t, appManifest.Base.UpdateNotes, body["update_notes"])
	assert.Equal(t, appManifest.Base.MinCompat, body["min_compat"])
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestBundleManifestValidationRejectsBadDocuments walks the structural and registry checks one at a
// time. A bad manifest is a build defect, and every one of these mutations used to be accepted
// silently (missing file → hardcoded 1.0.0, unvalidated code → path join).
func TestBundleManifestValidationRejectsBadDocuments(t *testing.T) {
	base := func() bundleManifest {
		raw, err := json.Marshal(appManifest)
		require.NoError(t, err)
		var m bundleManifest
		require.NoError(t, json.Unmarshal(raw, &m))
		return m
	}
	require.NoError(t, appManifestErr, "mutations below are relative to a valid manifest")

	tests := []struct {
		name   string
		mutate func(*bundleManifest)
		want   string
	}{
		{name: "missing generated_at", mutate: func(m *bundleManifest) { m.GeneratedAt = "" }, want: "generated_at"},
		{name: "version is not semver", mutate: func(m *bundleManifest) { m.Base.Version = "1.0" }, want: "base.version"},
		{name: "min_compat is not semver", mutate: func(m *bundleManifest) { m.Base.MinCompat = "latest" }, want: "base.min_compat"},
		{name: "size is zero", mutate: func(m *bundleManifest) { m.Base.Size = 0 }, want: "base.size"},
		{name: "sha256 is short", mutate: func(m *bundleManifest) { m.Base.SHA256 = "abc" }, want: "base.sha256"},
		{name: "url escapes /releases/", mutate: func(m *bundleManifest) { m.Base.URL = "https://cdn.example.com/x.zip" }, want: "base.url"},
		{name: "url traverses", mutate: func(m *bundleManifest) { m.Base.URL = "/releases/../etc/passwd" }, want: "base.url"},
		{name: "update notes empty", mutate: func(m *bundleManifest) { m.Base.UpdateNotes = "" }, want: "base.update_notes"},
		{name: "bundle code homeos", mutate: func(m *bundleManifest) {
			m.Bundles[0].Code = registry.HomeosCode
			m.Bundles[0].URL = m.Base.URL
		}, want: "homeos"},
		{name: "bundle code unknown", mutate: func(m *bundleManifest) { m.Bundles[0].Code = "does-not-exist" }, want: "registry"},
		{name: "bundle code unborn", mutate: func(m *bundleManifest) { m.Bundles[0].Code = "purchase" }, want: "尚未出生"},
		{name: "duplicate bundle", mutate: func(m *bundleManifest) { m.Bundles = append(m.Bundles, m.Bundles[0]) }, want: "重复"},
		{name: "bundle missing from manifest", mutate: func(m *bundleManifest) { m.Bundles = nil }, want: "不一致"},
		{name: "bundle sha256 bad", mutate: func(m *bundleManifest) { m.Bundles[0].SHA256 = "placeholder" }, want: "bundles[0].sha256"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base()
			tt.mutate(&m)
			err := m.validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

// TestBundleEndpointsFailClosed proves the 503 branch: an unusable manifest must not degrade into a
// guessed version or an empty list that the shell would read as "no faces".
func TestBundleEndpointsFailClosed(t *testing.T) {
	saved := appManifestErr
	appManifestErr = errors.New("broken for test")
	t.Cleanup(func() { appManifestErr = saved })

	for name, h := range map[string]gin.HandlerFunc{
		"bundles": GetAppBundles,
		"version": GetAppVersion,
	} {
		t.Run(name, func(t *testing.T) {
			w := callAppHandler(t, "/api/homeos/app/"+name, "", h)
			require.Equal(t, http.StatusServiceUnavailable, w.Code)

			var body map[string]string
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, "bundle_manifest_invalid", body["error"])
		})
	}
}
