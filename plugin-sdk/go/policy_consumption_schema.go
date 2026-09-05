package pluginsdk

import _ "embed"

//go:embed schema/policy-consumption-v1.schema.json
var policyConsumptionSchemaV1 []byte

// PolicyConsumptionSchemaV1 returns the canonical JSON payload schema. Dynamic
// authority, CAS, byte budgets and desired/applied invariants also require the
// public client/Host validators; schema validation alone cannot grant access.
func PolicyConsumptionSchemaV1() []byte { return append([]byte(nil), policyConsumptionSchemaV1...) }
