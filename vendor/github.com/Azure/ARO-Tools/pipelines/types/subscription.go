package types

type SubscriptionProvisioning struct {
	DisplayName                   Value  `json:"displayName"`
	AIRSRegisteredUserPrincipalId *Value `json:"airsRegisteredUserPrincipalId,omitempty"`
	CertificateDomains            *Value `json:"certificateDomains,omitempty"`
	BackfillSubscriptionId        *Value `json:"backfillSubscriptionId,omitempty"`

	// RoleAssignmentParameters is a relative path to the .bicepparam file used to deploy the bootstrapping role-assignments
	// for this subscription. Keep in mind that once Ev2 marks a subscription as provisioned, this will not run, so the contents
	// of this ARM template cannot change over time as the template will not be re-executed on existing subscriptions.
	RoleAssignmentParameters string `json:"roleAssignment,omitempty"`
}

func (s *SubscriptionProvisioning) Validate() error {
	return nil
}
