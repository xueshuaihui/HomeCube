// homeos_due_test.go pins the B 区 read side's argument contract at the repo boundary.
//
// The two filters themselves are falsified where they are observable -- through the 首屏 response, in
// handler/home_summary_test.go's TestHomeSummaryDueTodayDropsRevokedRegistrations and
// TestHomeSummaryDueTodayIsFilteredToTheMemberMountedFaces. What belongs here is the one thing a
// response cannot show: an EMPTY face-code set is refused instead of being answered as 「今日 0 项」.
// PRD 17.8's rule says the aggregate is filtered by the 已挂载面集合; it does not say a missing set
// means an empty day, and printing a real-looking 0 for a caller that forgot to wire the composition
// is exactly the 假数字 定版 ⑬ / 门禁「不写死假数据」 refuse. 该成员没有任何面 is expressible honestly
// (the caller passes just the 底座 code), so an empty list here can only be a wiring bug.
package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHomeSummaryDueTodayRefusesEmptyFaceSet(t *testing.T) {
	dayStart := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	window := DueWindow{
		DayStart: dayStart,
		DayEnd:   dayStart.AddDate(0, 0, 1),
	}

	// nil 是有意的：面集合的校验排在任何一次查询之前，所以接错线的请求不会先把库读一遍再答出一个 0。
	// 判据窗口/家庭 id 的既有前置校验顺带也被同一条路径挡住（familyID 非空、窗口有效）。
	for name, codes := range map[string][]string{"nil_set": nil, "empty_set": {}} {
		t.Run(name, func(t *testing.T) {
			_, err := HomeSummaryDueToday(context.Background(), nil, "22222222-2222-4222-8222-222222222222", window, codes)
			require.Error(t, err, "面集合缺失必须被拒，而不是当成「今日 0 项」答出来")
			assert.True(t, errors.Is(err, ErrInvalidArgument),
				"必须是可识别的参数错误（handler 走 internal 而不是把它画成一区数据），got %v", err)
		})
	}
}
