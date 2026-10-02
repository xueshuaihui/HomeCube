package obs

import (
	"log/slog"
	"os"

	"github.com/xueshuaihui/HomeCube/server/packages/registry"
)

// NewLogger returns the process-wide structured logger for one service.
//
// The two attributes are the naming dimensions §1.3 registers for this code -- code and
// svc-{code} -- taken from the registry row rather than spelled again here, so every line the
// process writes is attributable to exactly one service (PRD 21.5「当期在运的每个服务分别出图」,
// 22.2 第 10 条). Output is JSON on stdout: the process runs inside a container under `make up`
// (§10.1) and outside of it under `make dev-{code}` (§十三), and stdout is the one sink both
// shapes share.
//
// The family_id dimension of PRD 22.2 第 10 条 has no source in this checkout -- a request's family
// comes from the JWT claim the authz SDK middleware resolves (§4.1, PRD 14.5 第 3 项「token 携带
// family_id、角色快照与 pver」), which is S4. Attaching it here would mean an always-empty field,
// so it is reported as an open item instead of being stubbed.
//
// The document names no logging library; the standard library's log/slog is what this card picked,
// and that choice is listed as a document question.
func NewLogger(d registry.Domain) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})).With(
		slog.String("code", d.Code),
		slog.String("service", d.ServiceName),
	)
}
