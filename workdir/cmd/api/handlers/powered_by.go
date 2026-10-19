package handlers

import (
	"app/internal/pkg/plans"
	"app/internal/pkg/tenants"
)

// showPoweredBy reports whether the public form pages of a tenant on plan
// show the "Powered by gogoform" footer badge (the powered-by partial of
// templates/powered_by.html): free tenants show it, paid plans hide it.
func showPoweredBy(plan tenants.Plan) bool {
	return plans.For(plan).ShowBadge
}
