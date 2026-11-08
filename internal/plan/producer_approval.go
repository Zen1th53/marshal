package plan

// ProducerApprovalSeal records the exact producer alternatives reviewed by an
// owner. It is metadata, not a task/tool/transport grant. The canonical Store
// persists approval at ReviewedPlanVersion+1; any amendment needs new review.
type ProducerApprovalSeal struct {
	ReviewedPlanVersion int64        `json:"reviewed_plan_version"`
	Assignments         []Assignment `json:"assignments"`
}
