package handlers

import "context"

// showPoweredBy reports whether the public form pages of the tenant show the
// "Powered by gogoform" footer badge (the powered-by partial of
// templates/powered_by.html).
//
// TODO(000025): read the tenant's plan limits so paid plans can hide the
// badge; until plans exist every tenant shows it.
func showPoweredBy(ctx context.Context, tenantID int32) bool {
	return true
}
