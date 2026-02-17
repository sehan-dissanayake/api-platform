package policyv1alpha

const (
	// SystemParamConfigRefKey stores the config expression extracted from
	// wso2/defaultValue in policy systemParameters schemas.
	SystemParamConfigRefKey = "__wso2_internal_ref"

	// SystemParamDefaultValueKey stores the schema default value paired with
	// SystemParamConfigRefKey for runtime fallback on missing config keys.
	SystemParamDefaultValueKey = "__wso2_internal_default"

	// SystemParamRequiredKey stores whether the source schema marks the system
	// parameter as required at its object level.
	SystemParamRequiredKey = "__wso2_internal_required"
)
