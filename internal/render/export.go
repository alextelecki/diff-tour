package render

import (
	"math"
	"strings"

	"github.com/init-co-jp/diff-tour/internal/git"
	"github.com/init-co-jp/diff-tour/internal/tour"
)

// The HTML page draws the same blocks as the terminal. These wrappers let it reuse the choices made here,
// and keep the files shared with upstream unchanged.

// RoleOrder is the order file roles are listed in.
var RoleOrder = roleOrder

// PickHunks returns the hunks a diff block shows, or the message shown instead.
func PickHunks(ctx Ctx, ref *tour.DiffRef) ([]git.Hunk, string) { return pickHunks(ctx, ref) }

// RoleLabel is the name shown for a file role.
func RoleLabel(role string) string { return roleLabel(role) }

// SchemaMessage is the message shown when the schema cannot be loaded, without the ⚠ mark.
func SchemaMessage(err error) string {
	var b strings.Builder
	for i, l := range schemaError(err, math.MaxInt) {
		if i > 0 {
			b.WriteByte('\n')
		}
		for _, s := range l {
			b.WriteString(s.Text)
		}
	}
	return strings.TrimPrefix(b.String(), "⚠ ")
}
