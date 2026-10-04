package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xueshuaihui/HomeCube/server/packages/adapter/asr"
	svcauth "github.com/xueshuaihui/HomeCube/server/packages/auth"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/model"
	"github.com/xueshuaihui/HomeCube/server/services/svc-finance/internal/repo"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The request fixture has to carry real UUIDs: VoiceEntryRequest declares
// `binding:"required,uuid"` on family_id and account_id, so a non-UUID stand-in like
// "test-family-001" is rejected by ShouldBind and the handler returns 400 before any
// voice logic runs -- the test would then assert against a body the degrade branch never
// produced.
const (
	voiceFamilyUUID  = "11111111-1111-4111-8111-111111111111"
	voiceAccountUUID = "22222222-2222-4222-8222-222222222222"
)

// recordingASR is an asr.ASRAdapter test double that records every call.
//
// asr.StubAdapter cannot tell these branches apart: it returns "餐饮支出 50 元", nil for
// every input, so with the stub the ASR-failure path of VoiceEntry is unreachable and
// "TranscribeVoice was called / was not called" is unobservable. transcriptErr == nil
// gives the success branch, transcriptErr != nil gives the degrade branch.
type recordingASR struct {
	calls         int
	gotAudio      [][]byte
	transcript    string
	transcriptErr error
}

func (r *recordingASR) TranscribeVoice(_ context.Context, audioBytes []byte) (string, error) {
	r.calls++
	r.gotAudio = append(r.gotAudio, audioBytes)
	return r.transcript, r.transcriptErr
}

func setupVoiceTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)

	err = db.AutoMigrate(&model.FinanceTransaction{}, &model.FinanceAccount{}, &model.FinanceCategory{})
	assert.NoError(t, err)

	// VoiceEntry 现在按 `WHERE id = ? AND family_id = ?` 校验草稿账户的归属
	//（repo.AssertRefsExist）—— 引用的账户必须真实存在于本家庭，否则一律 404。
	require.NoError(t, db.Create(&model.FinanceAccount{
		ID:       voiceAccountUUID,
		FamilyID: voiceFamilyUUID,
		Name:     "语音记账账户",
		Type:     "cash",
	}).Error)

	return db
}

// newVoiceJSONRequest builds the JSON body VoiceEntry binds (no audio part).
func newVoiceJSONRequest(t *testing.T, familyID, accountID string) *http.Request {
	t.Helper()

	body, err := json.Marshal(map[string]string{
		"family_id":  familyID,
		"account_id": accountID,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/finance/voice-entry", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(bytes.NewReader(body))
	return req
}

// newVoiceMultipartRequest builds a multipart/form-data request carrying the bound form fields
// under the names the endpoint documents -- family_id / account_id / description -- plus, when
// audio is not empty, an "audio" file part. This is the only request shape that lets VoiceEntry
// reach TranscribeVoice, and it is the shape a real client sends: gin's form mapper resolves a form
// key as the `form` tag (handler/voice.go VoiceEntryRequest), so a body carrying the JSON names is
// what the ASR branch depends on. Sending the Go field names instead (FamilyID) would bind through
// gin's tag-less fallback and hide the defect this fixture exists to catch.
//
// Pass audio == "" for the no-file case: a multipart body of text fields only, which the handler is
// documented to degrade to a manual draft (handler/voice.go:60-67). An empty familyID or accountID
// means the client did not send that field at all, rather than sending it blank.
func newVoiceMultipartRequest(t *testing.T, familyID, accountID, description, audio string) *http.Request {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if familyID != "" {
		require.NoError(t, mw.WriteField("family_id", familyID))
	}
	if accountID != "" {
		require.NoError(t, mw.WriteField("account_id", accountID))
	}
	if description != "" {
		require.NoError(t, mw.WriteField("description", description))
	}
	if audio != "" {
		part, err := mw.CreateFormFile("audio", "voice.m4a")
		require.NoError(t, err)
		_, err = io.WriteString(part, audio)
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/finance/voice-entry", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// Voice entry with a valid request but no audio part: the handler must answer 200 with a
// manual draft instead of failing, and it must never spend an ASR call on empty input.
func TestVoiceHandler_VoiceEntry_ManualFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := &recordingASR{transcript: "餐饮支出 50 元"}
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// 家庭边界改造后 VoiceEntry 只认 session 的家庭（scopeFamily fail closed：无 session → 401）；
	// 请求里声明的 family_id 仅参与一致性校验。
	c.Set(svcauth.CtxSession, &svcauth.Session{
		AccountID: "voice-account",
		FamilyID:  voiceFamilyUUID,
		MemberID:  "voice-member",
		Role:      "owner",
	})
	c.Request = newVoiceJSONRequest(t, voiceFamilyUUID, voiceAccountUUID)

	handler.VoiceEntry(c)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var response VoiceEntryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// source=manual comes from the no-audio degrade branch, and the draft is built from
	// the *validated* request: a request that had been rejected by binding would leave
	// these empty, and an ASR-backed draft would carry a parsed amount instead of 0.
	assert.Equal(t, "manual", response.Source)
	require.NotNil(t, response.Draft)
	assert.Equal(t, voiceFamilyUUID, response.Draft.FamilyID)
	assert.Equal(t, voiceAccountUUID, response.Draft.AccountID)
	assert.Equal(t, int64(0), response.Draft.AmountCents)
	assert.Equal(t, "手动输入", response.Draft.Description)
	assert.Equal(t, 0, asrAdapter.calls, "no audio uploaded -> ASR must not be called")
}

// The degrade branch the card names: audio uploaded, ASR attempted, ASR failed -> manual
// draft with 200. The ASR call counter is the evidence the request actually got there.
func TestVoiceHandler_VoiceEntry_ASRFailureFallsBackToManual(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := &recordingASR{transcriptErr: fmt.Errorf("asr: provider unavailable")}
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// 家庭边界改造后 VoiceEntry 只认 session 的家庭（scopeFamily fail closed：无 session → 401）；
	// 请求里声明的 family_id 仅参与一致性校验。
	c.Set(svcauth.CtxSession, &svcauth.Session{
		AccountID: "voice-account",
		FamilyID:  voiceFamilyUUID,
		MemberID:  "voice-member",
		Role:      "owner",
	})
	c.Request = newVoiceMultipartRequest(t, voiceFamilyUUID, voiceAccountUUID, "", "fake-audio-bytes")

	handler.VoiceEntry(c)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var response VoiceEntryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	assert.Equal(t, 1, asrAdapter.calls, "请求必须真的走到 TranscribeVoice")
	require.Len(t, asrAdapter.gotAudio, 1)
	assert.NotEmpty(t, asrAdapter.gotAudio[0], "上传的音频字节必须被读出来交给 ASR")

	assert.Equal(t, "manual", response.Source)
	assert.Empty(t, response.Transcript, "ASR 失败时没有转写文本可回显")
	require.NotNil(t, response.Draft)
	assert.Equal(t, voiceFamilyUUID, response.Draft.FamilyID)
	assert.Equal(t, voiceAccountUUID, response.Draft.AccountID)
}

// The other side of the same branch: when ASR succeeds the response is source=asr with a
// parsed draft. This is what makes the two assertions above mean "degraded" rather than
// "the handler always answers manual".
func TestVoiceHandler_VoiceEntry_ASRSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := &recordingASR{transcript: "餐饮支出 50 元"}
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// 家庭边界改造后 VoiceEntry 只认 session 的家庭（scopeFamily fail closed：无 session → 401）；
	// 请求里声明的 family_id 仅参与一致性校验。
	c.Set(svcauth.CtxSession, &svcauth.Session{
		AccountID: "voice-account",
		FamilyID:  voiceFamilyUUID,
		MemberID:  "voice-member",
		Role:      "owner",
	})
	c.Request = newVoiceMultipartRequest(t, voiceFamilyUUID, voiceAccountUUID, "", "fake-audio-bytes")

	handler.VoiceEntry(c)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var response VoiceEntryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	assert.Equal(t, 1, asrAdapter.calls)
	assert.Equal(t, "asr", response.Source)
	assert.Equal(t, "餐饮支出 50 元", response.Transcript)
	require.NotNil(t, response.Draft)
	assert.Equal(t, int64(-5000), response.Draft.AmountCents, "转写文本被解析成草稿金额")
}

// The card's reachability proof, and the test that fails without the `form` tags: a real multipart
// client posts the documented field names (family_id / account_id) with an audio part, and the ASR
// branch must actually execute. ShouldBind's form mapper is what decides this -- with only `json`
// tags on VoiceEntryRequest the two fields bind to nothing, `required,uuid` rejects the request with
// 400 at handler/voice.go:53, and c.FormFile("audio") plus TranscribeVoice are unreachable.
//
// The draft carrying the posted family_id / account_id is what separates "the fields bound" from
// "the handler answered 200": a blank req would still produce a draft, just one with empty ids.
func TestVoiceHandler_VoiceEntry_MultipartFormFieldsBindAndDriveASR(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := &recordingASR{transcript: "餐饮支出 50 元"}
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// 家庭边界改造后 VoiceEntry 只认 session 的家庭（scopeFamily fail closed：无 session → 401）；
	// 请求里声明的 family_id 仅参与一致性校验。
	c.Set(svcauth.CtxSession, &svcauth.Session{
		AccountID: "voice-account",
		FamilyID:  voiceFamilyUUID,
		MemberID:  "voice-member",
		Role:      "owner",
	})
	c.Request = newVoiceMultipartRequest(t, voiceFamilyUUID, voiceAccountUUID, "午饭", "fake-audio-bytes")

	handler.VoiceEntry(c)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var response VoiceEntryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	// Exactly one ASR call: the branch ran once, on the uploaded bytes, and nothing else answered.
	require.Equal(t, 1, asrAdapter.calls, "multipart 请求必须真的进入 ASR 分支")
	require.Len(t, asrAdapter.gotAudio, 1)
	assert.Equal(t, []byte("fake-audio-bytes"), asrAdapter.gotAudio[0],
		"audio 部分的字节要整段交给 ASR 适配器")

	assert.Equal(t, "asr", response.Source)
	assert.Equal(t, "餐饮支出 50 元", response.Transcript, "transcript 来自 ASR 适配器")

	require.NotNil(t, response.Draft)
	assert.Equal(t, voiceFamilyUUID, response.Draft.FamilyID, "表单字段 family_id 已绑定进请求")
	assert.Equal(t, voiceAccountUUID, response.Draft.AccountID, "表单字段 account_id 已绑定进请求")
	assert.Equal(t, int64(-5000), response.Draft.AmountCents)
}

// Multipart with text fields and NO audio part: the degrade branch is documented (handler/voice.go
// is the fallback semantics), so this answers 200 with a manual draft built from the bound fields --
// and it must not spend an ASR call. The description override proving it bound is the difference
// from the JSON no-audio case above.
func TestVoiceHandler_VoiceEntry_MultipartWithoutAudioPartFallsBackToManual(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := &recordingASR{transcript: "餐饮支出 50 元"}
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// 家庭边界改造后 VoiceEntry 只认 session 的家庭（scopeFamily fail closed：无 session → 401）；
	// 请求里声明的 family_id 仅参与一致性校验。
	c.Set(svcauth.CtxSession, &svcauth.Session{
		AccountID: "voice-account",
		FamilyID:  voiceFamilyUUID,
		MemberID:  "voice-member",
		Role:      "owner",
	})
	c.Request = newVoiceMultipartRequest(t, voiceFamilyUUID, voiceAccountUUID, "打车给司机 30 元", "")

	handler.VoiceEntry(c)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var response VoiceEntryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))

	assert.Equal(t, "manual", response.Source)
	assert.Equal(t, 0, asrAdapter.calls, "没有 audio 部分就不许调用 ASR")
	// The description came from the form field, not from the "手动输入" placeholder.
	assert.Equal(t, "打车给司机 30 元", response.Transcript)
	require.NotNil(t, response.Draft)
	assert.Equal(t, "打车给司机 30 元", response.Draft.Description)
	assert.Equal(t, voiceFamilyUUID, response.Draft.FamilyID)
	assert.Equal(t, voiceAccountUUID, response.Draft.AccountID)
	assert.Equal(t, int64(0), response.Draft.AmountCents)
}

// The validation still bites on the multipart shape: with no family_id field in the body at all, the
// request is a 400 and the ASR adapter is never called -- the form tags widen what binds, they do not
// weaken `required,uuid`.
func TestVoiceHandler_VoiceEntry_MultipartMissingFamilyIDIsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := &recordingASR{transcript: "餐饮支出 50 元"}
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// 家庭边界改造后 VoiceEntry 只认 session 的家庭（scopeFamily fail closed：无 session → 401）；
	// 请求里声明的 family_id 仅参与一致性校验。
	c.Set(svcauth.CtxSession, &svcauth.Session{
		AccountID: "voice-account",
		FamilyID:  voiceFamilyUUID,
		MemberID:  "voice-member",
		Role:      "owner",
	})
	c.Request = newVoiceMultipartRequest(t, "", voiceAccountUUID, "", "fake-audio-bytes")

	handler.VoiceEntry(c)

	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, 0, asrAdapter.calls)
	assert.NotContains(t, w.Body.String(), `"draft"`, "绑定失败时不得返回草稿")
}

// A non-UUID family id must be refused: the required,uuid tag is a contract, and the two
// tests above only reach the voice branches because their fixture satisfies it.
func TestVoiceHandler_VoiceEntry_RejectsNonUUIDFamily(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := &recordingASR{transcript: "餐饮支出 50 元"}
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// 家庭边界改造后 VoiceEntry 只认 session 的家庭（scopeFamily fail closed：无 session → 401）；
	// 请求里声明的 family_id 仅参与一致性校验。
	c.Set(svcauth.CtxSession, &svcauth.Session{
		AccountID: "voice-account",
		FamilyID:  voiceFamilyUUID,
		MemberID:  "voice-member",
		Role:      "owner",
	})
	c.Request = newVoiceJSONRequest(t, "test-family-001", voiceAccountUUID)

	handler.VoiceEntry(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, asrAdapter.calls)
}

func TestVoiceHandler_ParseTranscriptToDraft(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := asr.NewStubAdapter()
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	tests := []struct {
		name           string
		transcript     string
		expectedType   string
		expectedAmount int64
	}{
		{"expense with amount", "餐饮支出 50 元", "expense", -5000},
		{"income with amount", "工资收入 1000 元", "income", 100000},
		{"decimal amount", "购物 29.9 元", "expense", -2990},
		{"no amount", "餐饮支出", "expense", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := VoiceEntryRequest{
				FamilyID:  "test-family",
				AccountID: "test-account",
			}

			draft, err := handler.parseTranscriptToDraft(tt.transcript, req, req.FamilyID)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedType, draft.Type)
			assert.Equal(t, tt.expectedAmount, draft.AmountCents)
		})
	}
}

func TestVoiceHandler_ExtractCategory(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db := setupVoiceTestDB(t)
	financeRepo := repo.NewFinanceRepo(db)
	asrAdapter := asr.NewStubAdapter()
	handler := NewVoiceHandler(financeRepo, asrAdapter)

	tests := []struct {
		transcript string
		category   string
	}{
		{"餐饮支出 50 元", "餐饮"},
		{"交通费用 30 元", "交通"},
		{"购物消费 100 元", "购物"},
		{"娱乐活动 80 元", "娱乐"},
		{"医疗费用 200 元", "医疗"},
		{"教育培训 500 元", "教育"},
		{"住房租金 3000 元", "住房"},
		{"水电费 150 元", "水电"},
		{"其他支出", ""},
	}

	for _, tt := range tests {
		t.Run(tt.transcript, func(t *testing.T) {
			category := handler.extractCategory(tt.transcript)
			assert.Equal(t, tt.category, category)
		})
	}
}

func TestContainsAny(t *testing.T) {
	tests := []struct {
		s       string
		substrs []string
		want    bool
	}{
		{"餐饮支出 50 元", []string{"收入", "收款"}, false},
		{"工资收入 1000 元", []string{"收入", "收款"}, true},
		{"转账 500 元", []string{"转账", "转出"}, true},
		{"", []string{"test"}, false},
	}

	for _, tt := range tests {
		result := containsAny(tt.s, tt.substrs)
		assert.Equal(t, tt.want, result)
	}
}
