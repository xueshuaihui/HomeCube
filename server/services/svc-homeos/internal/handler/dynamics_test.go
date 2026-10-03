// dynamics_test.go pins 动态流's pair (GET /dynamics, POST /dynamics/read) against the path blocks this
// card adds to contracts/openapi/homeos.yaml, and against the two PRD lines that decide its shape:
//
//   - 17.2 「与 GET /api/homeos/home/summary 同一个口径」: the feed's item cells are the SAME projection
//     the 首页's D 区 emits (handler.DynamicCell, six keys), asserted across the HTTP boundary by
//     comparing the two responses' key sets and their leading ids. One mapper per consumer is exactly
//     where a feed starts telling 首页 and 动态流 page different stories about one event, so the identity
//     is a test and not a comment.
//   - 17.1 第 2 条 + 15.2: read state is per MEMBER. The receipt table (homeos_dynamic_read) exists
//     precisely so one member's 「全部已读」 cannot clear another's 未读, and the member id is taken from
//     the session in every predicate. "Untouched" is therefore read back out of the database with
//     repo.CountUnreadDynamics rather than inferred from a response the same member produced -- a
//     response can only show what the caller was told, while the query shows what the row set is.
//
// The ?code= tests split the two halves of 17.8's 动态流筛选: the filter OPTION list is server-derived and
// comes from home/summary's faces[] (mountedFaces), so an unborn face never appears there -- while the
// filter VALUE is validated against registry (PRD 16.1 命名唯一真源), so an invented code is a 400 and a
// registered-but-unborn or 停用 code is a legitimate empty page. 17.8 「停用不删数据」 is why the second
// half must stay open: a switched-off face's history has to remain filterable, and the only writer in
// this checkout stamps code="homeos" (repo.CreateFamily), which is not a mountable 面 at all.
package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-homeos/internal/repo"
)

// addDynamicReadTable is 0007's homeos_dynamic_read (migrations/homeos/
// homeos_0007_home_projection.up.sql). auth_test.go's identity fixture creates homeos_dynamic but not
// this one, because no endpoint read receipts until now; UNIQUE(dynamic_id, member_id) is reproduced
// rather than left out, since repo.MarkDynamicRead writes through
// ON CONFLICT (dynamic_id, member_id) DO NOTHING and without the constraint SQLite would raise on a
// replayed id -- i.e. the idempotence the receipt table exists for would be untestable.
func addDynamicReadTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(
		`CREATE TABLE homeos_dynamic_read (
			id TEXT PRIMARY KEY, family_id TEXT NOT NULL, dynamic_id TEXT NOT NULL,
			member_id TEXT NOT NULL, read_at DATETIME NOT NULL, created_at DATETIME NOT NULL,
			UNIQUE(dynamic_id, member_id))`).Error)
}

// seedFeedRow writes one feed entry through the repository's own writer -- the seam the bus subscriber
// uses (repo.AppendDynamic's doc: 「never from a request handler」) -- and returns the row with the id
// AppendDynamic assigned, because the id-based POST tests need to name entries. Timestamps come in as
// Go instants so the stored column and the cursor parameter share one rendering (see
// home_summary_test.go's setFamilyTimezoneUTC note about the SQLite driver binding offsets).
func seedFeedRow(t *testing.T, db *gorm.DB, familyID, code, actor, action, summary string, at time.Time) *model.HomeosDynamic {
	t.Helper()
	row := &model.HomeosDynamic{
		FamilyID:  familyID,
		Code:      code,
		ActorName: actor,
		Action:    action,
		Summary:   summary,
		Entity:    "transaction",
		At:        at.UTC(),
	}
	require.NoError(t, repo.AppendDynamic(t.Context(), db, row))
	return row
}

func seedFamilyFeed(t *testing.T, db *gorm.DB, familyID string, n int) []*model.HomeosDynamic {
	t.Helper()
	code := registry.HomeosCode
	rows := make([]*model.HomeosDynamic, 0, n)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < n; i++ {
		rows = append(rows, seedFeedRow(t, db, familyID, code, "小明", "family.created",
			"创建了家庭 真实家庭", base.Add(time.Duration(i)*time.Minute)))
	}
	return rows
}

func readReceiptCount(t *testing.T, db *gorm.DB, memberID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM homeos_dynamic_read WHERE member_id = ?`, memberID).Scan(&n).Error)
	return n
}

func unreadDynamics(t *testing.T, db *gorm.DB, familyID, memberID string) int64 {
	t.Helper()
	n, err := repo.CountUnreadDynamics(t.Context(), db, familyID, memberID, nil)
	require.NoError(t, err)
	return n
}

// ==================== 同源：动态流与首页 D 区是一个流 ====================

// TestDynamicsFeedAndHomeSummaryServeOneStream is the identity 17.2 asks for: same rows, same order,
// same item cells.
func TestDynamicsFeedAndHomeSummaryServeOneStream(t *testing.T) {
	db, ownerAccount, familyID, _ := setupIdentityDB(t)
	addSummaryReadTables(t, db)
	addDynamicReadTable(t, db)
	s := newServices(t, db)
	r := newFeedRouter(t, s)
	token := sessionToken(t, s, ownerAccount, familyID, "owner")

	seedFamilyFeed(t, db, familyID, 2)
	// No homeos_family_module row is mounted: an empty 面集合 is a legal 首页 (17.8 的强制选面 is a
	// POST-time rule and faces[] simply comes back shorter), and this comparison is about the D 区 and
	// the feed agreeing, which needs no face at all.

	feed := doJSON(t, r, http.MethodGet, dynamicsPath, token, nil)
	require.Equal(t, http.StatusOK, feed.Code, feed.Body.String())
	var feedResp DynamicsResponse
	require.NoError(t, json.Unmarshal(feed.Body.Bytes(), &feedResp))
	require.Len(t, feedResp.Items, 2)

	feedKeys := jsonKeys(t, feed.Body.Bytes())
	assert.ElementsMatch(t, []string{"items", "next_cursor"}, keysOf(feedKeys),
		"GET /dynamics 顶层只有 items 与 next_cursor：%s", feed.Body.String())

	summary := doJSON(t, r, http.MethodGet, summaryPath, token, nil)
	require.Equal(t, http.StatusOK, summary.Code, summary.Body.String())
	var summaryDoc struct {
		Dynamics struct {
			Items []map[string]json.RawMessage `json:"items"`
		} `json:"dynamics"`
	}
	require.NoError(t, json.Unmarshal(summary.Body.Bytes(), &summaryDoc))
	require.NotEmpty(t, summaryDoc.Dynamics.Items, "D 区有数据，本测试才有可比对象")

	// Both sides are decoded as generic objects rather than through the Go structs, because the claim
	// under test is what the WIRE says about each consumer's newest entry.
	feedItems := decodeItems(t, feed.Body.Bytes())
	summaryItems := summaryDoc.Dynamics.Items
	assert.Equal(t, string(feedItems[0]["id"]), string(summaryItems[0]["id"]),
		"两个消费位的最新一条必须是同一条动态（同序，17.2 「同一个口径」）")
	assert.ElementsMatch(t, keysOf(feedItems[0]), keysOf(summaryItems[0]),
		"dynamics.items[] 的字段集必须与 home/summary 的 D 区一致")
	assert.ElementsMatch(t, []string{"action", "actor_name", "at", "code", "id", "summary"}, keysOf(feedItems[0]),
		"且两侧同时等于契约声明的六键（id/code/actor_name/action/summary/at）")
}

// decodeItems reads a list response's items as generic objects, so the assertions below are about the
// wire rather than about the Go struct that also happens to describe it.
func decodeItems(t *testing.T, body []byte) []map[string]json.RawMessage {
	t.Helper()
	var doc struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(body, &doc))
	return doc.Items
}

// ==================== 读态按成员隔离 ====================

// TestDynamicsReadStateIsPerMember is the reason homeos_dynamic_read exists: a boolean column on
// homeos_dynamic would let one member's 「全部已读」 clear the whole family's 未读, i.e. a read endpoint
// writing another member's state.
func TestDynamicsReadStateIsPerMember(t *testing.T) {
	db, ownerAccount, familyID, ownerMember := setupIdentityDB(t)
	addDynamicReadTable(t, db)
	s := newServices(t, db)
	r := newFeedRouter(t, s)

	rows := seedFamilyFeed(t, db, familyID, 3)
	memberAccount := addAccountWithRole(t, db, familyID, "13800000061", "大哥", "member")
	memberID2 := memberIDForAccount(t, db, familyID, memberAccount)

	ownerToken := sessionToken(t, s, ownerAccount, familyID, "owner")
	memberToken := sessionToken(t, s, memberAccount, familyID, "member")

	// Nobody has read anything yet: the same family, two members, one identical unread figure each --
	// counted server-side.
	require.Equal(t, int64(3), unreadDynamics(t, db, familyID, ownerMember))
	require.Equal(t, int64(3), unreadDynamics(t, db, familyID, memberID2))

	// ① 全部已读 for the owner.
	w := doJSON(t, r, http.MethodPost, dynamicsReadPath, ownerToken, gin.H{"all": true})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var ownerMarked MarkDynamicsReadResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ownerMarked))
	assert.Equal(t, int64(3), ownerMarked.MarkedCount, "三条未读全部落回执")
	assert.Equal(t, int64(0), ownerMarked.Unread)
	assert.Equal(t, int64(3), readReceiptCount(t, db, ownerMember))

	// ② The second member's unread is untouched -- read from the table, not from a response.
	assert.Equal(t, int64(3), unreadDynamics(t, db, familyID, memberID2),
		"一个成员的已读不得改动另一个成员的未读（PRD 17.1 第 2 条、15.2）")
	assert.Zero(t, readReceiptCount(t, db, memberID2), "回执行按 member_id 分列")

	// ③ The second member marks ONE entry by id.
	w = doJSON(t, r, http.MethodPost, dynamicsReadPath, memberToken,
		gin.H{"dynamic_ids": []string{rows[1].ID}, "all": false})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var picked MarkDynamicsReadResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &picked))
	assert.Equal(t, int64(1), picked.MarkedCount)
	assert.Equal(t, int64(2), picked.Unread, "剩余未读服务端重读，不是 ids 数量的补数")
	assert.Equal(t, int64(1), readReceiptCount(t, db, memberID2))
	assert.Equal(t, int64(0), unreadDynamics(t, db, familyID, ownerMember), "本人的 0 不因他人而回升")

	// ④ Same id again: the unique receipt index makes it a no-op, reported as 0 rather than an error.
	w = doJSON(t, r, http.MethodPost, dynamicsReadPath, memberToken,
		gin.H{"dynamic_ids": []string{rows[1].ID}, "all": false})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var replay MarkDynamicsReadResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &replay))
	assert.Zero(t, replay.MarkedCount)
	assert.Equal(t, int64(2), replay.Unread)
	assert.Equal(t, int64(1), readReceiptCount(t, db, memberID2), "重放不得增加回执行")

	// ⑤ An id that is not in this family: 404, and the whole request rolls back -- a typo in a list of
	//    three must not silently become "marked the two that existed".
	before := readReceiptCount(t, db, memberID2)
	w = doJSON(t, r, http.MethodPost, dynamicsReadPath, memberToken,
		gin.H{"dynamic_ids": []string{rows[0].ID, "00000000-0000-4000-8000-000000000000"}, "all": false})
	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	assert.Contains(t, string(jsonKeys(t, w.Body.Bytes())["error"]), "dynamic_not_found")
	assert.Equal(t, before, readReceiptCount(t, db, memberID2), "404 之后一条回执都没写")

	// ⑥ Read state filters nothing out of the stream by default: both members still see all three rows.
	for _, pair := range []struct{ token, who string }{{ownerToken, "owner"}, {memberToken, "member"}} {
		list := doJSON(t, r, http.MethodGet, dynamicsPath, pair.token, nil)
		require.Equal(t, http.StatusOK, list.Code, list.Body.String())
		assert.Len(t, decodeItems(t, list.Body.Bytes()), 3, "%s 的列表不受他人已读影响", pair.who)
	}

	// ⑦ No explicit range is a 400 rather than an accidental inbox-clear.
	ambiguous := doJSON(t, r, http.MethodPost, dynamicsReadPath, memberToken, gin.H{})
	require.Equal(t, http.StatusBadRequest, ambiguous.Code, ambiguous.Body.String())
}

// ==================== 面筛选 ====================

// TestDynamicsCodeFilterIsRegistryValidatedAndNeverInventsOptions covers three separate readings of
// 17.8's 动态流筛选: an unregistered code is refused, a registered code (including the 底座's own "homeos",
// which is the only value this checkout's writer emits) filters, and a registered-but-unborn face is a
// legal empty page rather than a refusal -- while that same unborn face must never show up as a filter
// OPTION, because options come from home/summary's faces[] (mountedFaces).
func TestDynamicsCodeFilterIsRegistryValidatedAndNeverInventsOptions(t *testing.T) {
	db, ownerAccount, familyID, _ := setupIdentityDB(t)
	addSummaryReadTables(t, db)
	addDynamicReadTable(t, db)
	s := newServices(t, db)
	r := newFeedRouter(t, s)
	token := sessionToken(t, s, ownerAccount, familyID, "owner")

	born := firstBornFace(t)
	unborn := firstUnbornFace(t)
	base := time.Now().UTC().Add(-time.Hour)
	seedFeedRow(t, db, familyID, registry.HomeosCode, "小明", "family.created", "创建了家庭 真实家庭", base)
	seedFeedRow(t, db, familyID, born.Code, "小明", "transaction.created", "记了一笔 餐饮", base.Add(time.Minute))

	all := doJSON(t, r, http.MethodGet, dynamicsPath, token, nil)
	require.Equal(t, http.StatusOK, all.Code, all.Body.String())
	require.Len(t, decodeItems(t, all.Body.Bytes()), 2, "缺省 code 返回整个家庭的流")

	filtered := doJSON(t, r, http.MethodGet, dynamicsPath+"?code="+born.Code, token, nil)
	require.Equal(t, http.StatusOK, filtered.Code, filtered.Body.String())
	items := decodeItems(t, filtered.Body.Bytes())
	require.Len(t, items, 1)
	assert.Contains(t, string(items[0]["code"]), born.Code)

	bottom := doJSON(t, r, http.MethodGet, dynamicsPath+"?code="+registry.HomeosCode, token, nil)
	require.Equal(t, http.StatusOK, bottom.Code, bottom.Body.String())
	assert.Len(t, decodeItems(t, bottom.Body.Bytes()), 1, "底座自己写入的 family.created 必须仍可筛选，否则唯一一类真实动态永远取不到")

	// A code registry does not know is a 400 (PRD 16.1 命名唯一真源), not an empty page that quietly
	// proves nothing.
	invented := doJSON(t, r, http.MethodGet, dynamicsPath+"?code=not_a_face", token, nil)
	require.Equal(t, http.StatusBadRequest, invented.Code, invented.Body.String())
	assert.Contains(t, string(jsonKeys(t, invented.Body.Bytes())["error"]), "unknown_module")

	// Registered but unborn: legal, and legitimately empty. The 未出生面 rule lives on the OPTION side,
	// which is the next assertion.
	early := doJSON(t, r, http.MethodGet, dynamicsPath+"?code="+unborn.Code, token, nil)
	require.Equal(t, http.StatusOK, early.Code, early.Body.String())
	assert.Empty(t, decodeItems(t, early.Body.Bytes()))

	modules := doJSON(t, r, http.MethodGet, modulesPath, token, nil)
	require.Equal(t, http.StatusOK, modules.Code, modules.Body.String())
	var catalog FamilyModulesResponse
	require.NoError(t, json.Unmarshal(modules.Body.Bytes(), &catalog))
	assert.GreaterOrEqual(t, len(catalog.Modules), 1, "面目录非空，下面的「未出现」断言才有意义")

	summary := doJSON(t, r, http.MethodGet, summaryPath, token, nil)
	require.Equal(t, http.StatusOK, summary.Code, summary.Body.String())
	var doc struct {
		Faces []struct {
			Code string `json:"code"`
		} `json:"faces"`
	}
	require.NoError(t, json.Unmarshal(summary.Body.Bytes(), &doc))
	codes := make([]string, 0, len(doc.Faces))
	for _, f := range doc.Faces {
		codes = append(codes, f.Code)
	}
	assert.NotContains(t, codes, unborn.Code,
		"未出生面不得作为动态流筛选选项出现（17.8：可选项来自服务端合成的 faces[]，客户端不自造列表）")
}

// ==================== 游标 / limit ====================

func TestDynamicsCursorPagesAndRefusesForgedTokens(t *testing.T) {
	db, ownerAccount, familyID, _ := setupIdentityDB(t)
	addDynamicReadTable(t, db)
	s := newServices(t, db)
	r := newFeedRouter(t, s)
	token := sessionToken(t, s, ownerAccount, familyID, "owner")

	seedFamilyFeed(t, db, familyID, 3)

	first := doJSON(t, r, http.MethodGet, dynamicsPath+"?limit=2", token, nil)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var firstResp DynamicsResponse
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstResp))
	require.Len(t, firstResp.Items, 2)
	require.NotNil(t, firstResp.NextCursor, "还有下一页就必须给出令牌")

	second := doJSON(t, r, http.MethodGet, dynamicsPath+"?limit=2&cursor="+*firstResp.NextCursor, token, nil)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	var secondResp DynamicsResponse
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondResp))
	require.Len(t, secondResp.Items, 1)
	assert.Nil(t, secondResp.NextCursor, "取完即无游标")
	assert.NotEqual(t, secondResp.Items[0].ID, firstResp.Items[0].ID, "下一页不得重播上一条")

	// Newest-first across the whole page, so the 动态流's ordering is a server fact.
	require.GreaterOrEqual(t, len(firstResp.Items), 2)
	assert.False(t, firstResp.Items[0].At.Before(firstResp.Items[1].At), "at DESC")

	forged := base64.RawURLEncoding.EncodeToString([]byte("2026-03-01T00:00:00Z"))
	bad := doJSON(t, r, http.MethodGet, dynamicsPath+"?cursor="+forged, token, nil)
	require.Equal(t, http.StatusBadRequest, bad.Code, bad.Body.String())
	keys := jsonKeys(t, bad.Body.Bytes())
	assert.Contains(t, string(keys["error"]), "invalid_cursor")
	assert.NotContains(t, keys, "items", "非法游标不回落首页，否则无限滚动会绕圈：%s", bad.Body.String())

	for _, raw := range []string{"?limit=0", "?limit=-1", "?limit=abc"} {
		w := doJSON(t, r, http.MethodGet, dynamicsPath+raw, token, nil)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%s 必须 400：%s", raw, w.Body.String())
	}
}

// TestDynamicsFeedIsFamilyScoped asserts the other half of 15.2: a second family's events are not in
// this session's stream, and its ids cannot be marked read through this session.
func TestDynamicsFeedIsFamilyScoped(t *testing.T) {
	db, ownerAccount, familyID, ownerMember := setupIdentityDB(t)
	addDynamicReadTable(t, db)
	s := newServices(t, db)
	r := newFeedRouter(t, s)
	token := sessionToken(t, s, ownerAccount, familyID, "owner")

	seedFamilyFeed(t, db, familyID, 1)
	otherFamily := "99999999-9999-4999-8999-999999999999"
	require.NoError(t, db.Exec(
		`INSERT INTO homeos_families (id, name, owner_id, timezone, currency, pver, created_at, updated_at)
		 VALUES (?, ?, ?, 'Asia/Shanghai', 'CNY', 1, datetime('now'), datetime('now'))`,
		otherFamily, "别的家庭", ownerAccount).Error)
	foreign := seedFeedRow(t, db, otherFamily, registry.HomeosCode, "别人", "family.created", "别的家庭创建了", time.Now().UTC())

	list := doJSON(t, r, http.MethodGet, dynamicsPath, token, nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	items := decodeItems(t, list.Body.Bytes())
	require.Len(t, items, 1, "另一个家庭的动态不进本会话的流（family_id 取自令牌）")

	cross := doJSON(t, r, http.MethodPost, dynamicsReadPath, token,
		gin.H{"dynamic_ids": []string{foreign.ID}, "all": false})
	require.Equal(t, http.StatusNotFound, cross.Code, cross.Body.String())
	assert.Equal(t, int64(0), readReceiptCount(t, db, ownerMember), "跨家庭的 id 既标不了也不留回执")
}
