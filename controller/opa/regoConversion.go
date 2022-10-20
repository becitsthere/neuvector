package opa

import (
	b64 "encoding/base64"
	"fmt"
	"github.com/neuvector/neuvector/share" // * official build
	log "github.com/sirupsen/logrus"
	"strings"
)

func convertRiskyRoleRule(rule *share.CLUSAdmissionRule) string {
	rego := []string{}

	// has custom criteria
	hasCusomCriteria := false
	for _, c := range rule.Criteria {
		if c.Type != "" {
			hasCusomCriteria = true
		}
	}

	if !hasCusomCriteria {
		return ""
	}

	// print header
	packageName := fmt.Sprintf("package neuvector_policy_%d", rule.ID)
	rego = append(rego, packageName)
	// rego = append(rego, printSpec())

	rego = append(rego, "specification = spec {")
	rego = append(rego, "	spec:={")
	rego = append(rego, `		"version": "v1",`)
	rego = append(rego, `		"description": "NeuVector generated",`)
	rego = append(rego, fmt.Sprintf(`		"comment": %q ,`, rule.Comment))
	rego = append(rego, fmt.Sprintf(`		"useAsRiskyRoleTag": %v ,`, rule.UseAsRiskyRoleTag))
	rego = append(rego, "	}")
	rego = append(rego, "}")

	for _, c := range rule.Criteria {
		if c.Op == "arrayContainsAny" && (strings.HasSuffix(c.Path, "rules[_].resources[_]") || strings.HasSuffix(c.Path, "rules[_].resources")) {
			line1 := `
get_parameter_resources(p) := x {
	p == "get"
				`
			rego = append(rego, line1)

			quotedString := parseQuotedString(c.Value)
			line2 := fmt.Sprintf("	x:= [%s]", strings.Join(quotedString, ","))
			rego = append(rego, line2)
			rego = append(rego, "}")
		} else if c.Op == "arrayContainsAny" && (strings.HasSuffix(c.Path, "rules[_].verbs[_]") || strings.HasSuffix(c.Path, "rules[_].verbs")) {
			line1 := `
get_parameter_verbs(p) := x {
	p == "get"
				`
			rego = append(rego, line1)

			quotedString := parseQuotedString(c.Value)
			line2 := fmt.Sprintf("	x:= [%s]", strings.Join(quotedString, ","))
			rego = append(rego, line2)
			rego = append(rego, "}")
		} else {
			////TODO: what is the rule contains other criteria?  like namespace?
		}
	}

	rego = append(rego, printRiskyRoleFuntions())
	regoStr := strings.Join(rego, "\n")

	policyUrl := formatPolicyUrl(rule.ID)
	success := AddPolicy(policyUrl, regoStr)

	// for risky role rule, add a mapping to link comment and ruleID
	AddRiskyRuleMapping(rule.Comment, int(rule.ID))

	log.WithFields(log.Fields{"policyUrl": policyUrl, "success": success}).Debug("Add Policy")

	if !success {
		// unable to add the rego
		// write another version of Rego of all comment to ensure it will success
		rego2 := []string{}
		rego2 = append(rego2, packageName)
		rego2 = append(rego2, "############# DEBUG VERSION #############")

		items := strings.Split(regoStr, "\n")
		for _, v := range items {
			rego2 = append(rego2, "#! "+v)
		}

		AddPolicy(policyUrl, strings.Join(rego2, "\n"))

		log.WithFields(log.Fields{"policyUrl": policyUrl}).Debug("Add Policy with all comments.")
	}

	return regoStr
}

// TODO: for podTemplate, we need to handle all resources have pod
//
//	deployment, daemonset, statefulset, cronjob, job, etc... (total is 8)
func ConvertToRegoRule(rule *share.CLUSAdmissionRule) string {
	rego := []string{}

	if rule.UseAsRiskyRoleTag {
		return convertRiskyRoleRule(rule)
	}

	log.WithFields(log.Fields{"rule": rule}).Debug("ConvertToRego")

	// has custom criteria
	hasCusomCriteria := false
	for _, c := range rule.Criteria {
		if c.Type != "" {
			hasCusomCriteria = true
		}
	}

	if !hasCusomCriteria {
		return ""
	}

	/////////////////////////////////////////
	// print header
	packageName := fmt.Sprintf("package neuvector_policy_%d", rule.ID)
	rego = append(rego, packageName)

	rego = append(rego, printSpec())

	// ===========================================
	// main
	mainFunc := `
violation[result]{
	request := _get_input("get")
	`

	rego = append(rego, mainFunc)

	for j, c := range rule.Criteria {
		if c.Type == "customPath" || c.Type == "saBindRiskyRole" {
			rego = append(rego, "\t"+convertCriteriaFunctionCall(j, c, true))
		}
	}

	mainFunc = `
	result:={
		"message": "all criteria have been met"
	}
}
	`
	rego = append(rego, mainFunc)

	// handling type=1 (general) individual criteria conversion
	for j, c := range rule.Criteria {
		if c.Type == "" {
			// if it's predefined then the type is empty string, we don't need to handle it
			continue
		}

		if c.Type == "saBindRiskyRole" && c.Op == "containsTagAny" {
			c_rego := convertRiskyRoleTagCriteria(j, c)
			rego = append(rego, c_rego...)
		}

		if c.Type == "customPath" {
			c_rego := convertGenericCriteria(j, c)
			rego = append(rego, c_rego...)
		}

		// // type="saBindRiskyRole"
		// if c.Op == "containsTagAny" {
		// 	c_rego := convertRiskyRoleTagCriteria(j, c)
		// 	rego = append(rego, c_rego...)
		// } else {
		// 	// type="customPath"
		// 	c_rego := convertGenericCriteria(j, c)
		// 	rego = append(rego, c_rego...)
		// }
	}

	// handling type=3 (raw rego) individual criteria conversion
	// for j, c := range rule.Criteria {
	// 	if c.Type == 3 {
	// 		// criteria_1(request){
	// 		// 		main(request)    // *** user's raw rego function
	// 		// }

	// 		rego = append(rego, fmt.Sprintf("criteria_%d(request){", j))
	// 		rego = append(rego, "	# calling user provided rego function")
	// 		rego = append(rego, "	main(request)")
	// 		rego = append(rego, "}\n")

	// 		rego = append(rego, "# custom raw rego (start) ====== ")
	// 		rego = append(rego, c.Value)
	// 		rego = append(rego, "# custom raw rego (end) ====== ")
	// 	}
	// }

	// generate troubleshooting code
	// handling type=1 (general) keep track each individual criteria result
	for j, c := range rule.Criteria {
		if c.Type == "customPath" {
			info := fmt.Sprintf("check [%s] with [%s] op", c.Path, c.Op)

			rego = append(rego, `
violationmsgs[msg]{
	request := _get_input("get")
`)
			rego = append(rego, "\t"+convertCriteriaFunctionCall(j, c, true))
			rego = append(rego, fmt.Sprintf(`	msg:="criteria_%d met. (%s)"`, j, info))
			rego = append(rego, "}")

			rego = append(rego, `
violationmsgs[msg]{
	request := _get_input("get")
`)
			rego = append(rego, "	not "+convertCriteriaFunctionCall(j, c, true))
			rego = append(rego, fmt.Sprintf(`	msg:="criteria_%d not met. (%s)"`, j, info))
			rego = append(rego, "}")
		}
	}

	// helper functions
	rego = append(rego, printHelperFunctions())

	regoStr := strings.Join(rego, "\n")

	policyUrl := formatPolicyUrl(rule.ID)
	success := AddPolicy(policyUrl, regoStr)
	log.WithFields(log.Fields{"policyUrl": policyUrl, "success": success}).Debug("Add Policy")

	if !success {
		// unable to add the rego
		// write another version of Rego of all comment to ensure it will success

		rego2 := []string{}
		rego2 = append(rego2, packageName)
		rego2 = append(rego2, "############# THIS IS DEBUG VERSION #############")

		items := strings.Split(regoStr, "\n")
		for _, v := range items {
			rego2 = append(rego2, "#! "+v)
		}

		AddPolicy(policyUrl, strings.Join(rego2, "\n"))

		log.WithFields(log.Fields{"policyUrl": policyUrl}).Debug("Add Policy with all comments.")
	}

	return regoStr
}

func formatPolicyUrl(ruleID uint32) string {
	return fmt.Sprintf("/v1/policies/policy/rule_%d", ruleID)
}

func convertCriteriaFunctionCall(idx int, c *share.CLUSAdmRuleCriterion, withComment bool) string {

	if withComment {
		return fmt.Sprintf("criteria_%d(request)    # check [%s] with [%s]", idx, c.Path, c.Op)
	}

	return fmt.Sprintf("criteria_%d(request)", idx)
}

func convertGenericCriteria(idx int, c *share.CLUSAdmRuleCriterion) []string {

	rego := []string{}

	functionName := convertCriteriaFunctionCall(idx, c, false)

	rego = append(rego, functionName)
	rego = append(rego, "{")

	rego = append(rego, fmt.Sprintf("	# Custome Criteria name = %v", c.Name))
	rego = append(rego, fmt.Sprintf("	# op = %v", c.Op))
	rego = append(rego, fmt.Sprintf("	# value = %v", c.Value))
	rego = append(rego, fmt.Sprintf("	# valueType = %v", c.ValueType))
	rego = append(rego, fmt.Sprintf("	# path = %v\n", c.Path))

	// all ValueType (key, string, number, boolean) has "exist" and "notExist"
	if c.Op == "exist" || c.Op == "notExist" {
		path := c.Path
		prefix := ""

		if c.Op == "notExist" {
			prefix = "not"
		}

		if strings.HasPrefix(c.Path, "item.") {
			path = "request" + c.Path[4:]
		}

		rego = append(rego, fmt.Sprintf("	%s %s", prefix, path))

		rego = append(rego, "}")
		rego = append(rego, "\n")
	} else if c.ValueType == "string" {
		quotedString := parseQuotedString(c.Value)
		line := fmt.Sprintf("	user_provided_data := [%s]", strings.Join(quotedString, ","))
		rego = append(rego, line)

		if strings.HasPrefix(c.Path, "item.") {
			result := "request" + c.Path[4:]
			rego = append(rego, fmt.Sprintf("	value = %s", result))
		} else {
			rego = append(rego, fmt.Sprintf("	value = %s", c.Path))
		}

		if c.Op == "containsAll" {
			rego = append(rego, "	operator_contains_all(user_provided_data, value)")
		} else if c.Op == "containsAny" {
			rego = append(rego, "	operator_contains_any(user_provided_data, value)")
		} else if c.Op == "notContainsAny" {
			rego = append(rego, "	operator_not_contains_any(user_provided_data, value)")
		} else if c.Op == "containsOtherThan" {
			rego = append(rego, "	operator_contains_other_than(user_provided_data, value)")
		}

		rego = append(rego, "}")
		rego = append(rego, "\n")
	} else if c.ValueType == "number" {
		line := fmt.Sprintf("	user_provided_data := %s", c.Value)
		rego = append(rego, line)

		if strings.HasPrefix(c.Path, "item.") {
			result := "request" + c.Path[4:]
			rego = append(rego, fmt.Sprintf("	value = %s", result))
		} else {
			rego = append(rego, fmt.Sprintf("	value = %s", c.Path))
		}

		if c.Op == "=" {
			rego = append(rego, "	value == user_provided_data")
		} else if c.Op == "!=" {
			rego = append(rego, "	value != user_provided_data")
		} else if c.Op == ">=" {
			rego = append(rego, "	value >= user_provided_data")
		} else if c.Op == ">" {
			rego = append(rego, "	value >= user_provided_data")
		} else if c.Op == "<=" {
			rego = append(rego, "	value <= user_provided_data")
		}

		rego = append(rego, "}")
		rego = append(rego, "\n")
	} else if c.ValueType == "boolean" {
		line := fmt.Sprintf("	user_provided_data := %s", c.Value)
		rego = append(rego, line)

		if strings.HasPrefix(c.Path, "item.") {
			result := "request" + c.Path[4:]
			rego = append(rego, fmt.Sprintf("	value = %s", result))
		} else {
			rego = append(rego, fmt.Sprintf("	value = %s", c.Path))
		}

		if c.Op == "=" {
			rego = append(rego, "	value == user_provided_data")
		}

		rego = append(rego, "}")
		rego = append(rego, "\n")
	}

	return rego
}

func convertRiskyRoleTagCriteria(idx int, c *share.CLUSAdmRuleCriterion) []string {
	rego := []string{}

	if c.Op != "containsTagAny" {
		return rego
	}

	// this operator is used to check risky role tag generated by other rules
	// Operator == containsTagAny
	// Values == rules name we want to check, example: ["view_risky_secrets", "create_pod"]
	// Path == (should be default to ServiceAccountName, no need to specify ?)
	// get ruleID by name, example: "view_secret, create_pod"
	// needs to get their corresponding ruleID, for example 1004, 1005
	ruleIDs := []int{}
	items := strings.Split(c.Value, ",")
	for _, v := range items {
		getBackRuleId := GetRiskyRoleRuleIDByName(strings.TrimSpace(v))
		if getBackRuleId > 0 {
			ruleIDs = append(ruleIDs, getBackRuleId)
		}
	}

	log.WithFields(log.Fields{"criteria_values": c.Value, "ruleIDs": ruleIDs}).Debug("convertRiskyRoleTagCriteria")

	////////////////////////////////////
	// start rego generation part 1A (for regular rbac data check)
	functionName := convertCriteriaFunctionCall(idx, c, false)
	rego = append(rego, "# [part 1A] for regular RBAC data check (clusterrolebindings)")
	rego = append(rego, functionName)
	rego = append(rego, "{")
	rego = append(rego, fmt.Sprintf("	rulesToCheck := [%s]	# any rule met", arrayToString(ruleIDs, ",")))
	rego = append(rego, "	ruleId := rulesToCheck[_]")

	rego = append(rego, "	sa := request.spec.serviceAccountName")

	line := `
	# ==============================================
	# find corresponding cluster role binding
	crb :=  data.neuvector.k8s.clusterrolebindings[crb_name]

	subjects := getSubjects(crb)
    subject := subjects[i]

	subject.kind == "ServiceAccount"
	subject.name == sa`
	rego = append(rego, line)

	line = `
	# ==============================================
    # get corresponding role (clusterroles/roles) generated by the ruleId and check if it's in the list
	roleRef := getRoleRef(crb)
	
	roleRefKind := roleRef.kind
	roleName := roleRef.name
	
	violationRoles = get_risky_role_rule_data(roleRefKind, ruleId)
	roleName  == violationRoles[_]`
	rego = append(rego, line)
	rego = append(rego, "}\n")

	////////////////////////////////////
	// start rego generation part 1B (for configuratoin assessment rbac data check)
	rego = append(rego, "# [part 1B] for configuratoin assessment session data check (clusterrolebindings)")
	rego = append(rego, functionName)
	rego = append(rego, "{")
	rego = append(rego, fmt.Sprintf("	rulesToCheck := [%s]	# any rule met", arrayToString(ruleIDs, ",")))
	rego = append(rego, "	ruleId := rulesToCheck[_]")

	rego = append(rego, "	sa := request.spec.serviceAccountName")

	line = `
	# ==============================================
	# find corresponding cluster role binding
	crb :=  data.neuvector.k8s.clusterrolebindings[crb_name]

	subjects := getSubjects(crb)
    subject := subjects[i]

	subject.kind == "ServiceAccount"
	subject.name == sa`
	rego = append(rego, line)

	line = `
	# ==============================================
    # get corresponding role (clusterroles/roles) generated by the ruleId and check if it's in the list
	roleRef := getRoleRef(crb)
	
	roleRefKind := roleRef.kind
	roleName := roleRef.name
	
	violationRoles = get_risky_role_rule_data(roleRefKind, ruleId)
	# roleName  == violationRoles[_]

	violationRoleName = violationRoles[_]
    contains(violationRoleName, "_config_assessment_")		# format signature: 12345_config_assessment_
	violationRoleName2 := substring(violationRoleName, 24, -1)
    roleName == violationRoleName2`

	rego = append(rego, line)
	rego = append(rego, "}\n")

	////////////////////////////////////
	// start rego generation part 2A (for regular rbac data check)
	functionName = convertCriteriaFunctionCall(idx, c, false)
	rego = append(rego, "# [part 2A] for regular RBAC data check (rolebindings)")
	rego = append(rego, functionName)
	rego = append(rego, "{")
	rego = append(rego, fmt.Sprintf("	rulesToCheck := [%s]	# any rule met", arrayToString(ruleIDs, ",")))
	rego = append(rego, "	ruleId := rulesToCheck[_]")

	rego = append(rego, "	sa := request.spec.serviceAccountName")

	line = `
	# ==============================================
	# find corresponding role binding
	crb :=  data.neuvector.k8s.rolebindings[crb_name]

	subjects := getSubjects(crb)
    subject := subjects[i]

	subject.kind == "ServiceAccount"
	subject.name == sa`
	rego = append(rego, line)

	line = `
	# ==============================================
    # get corresponding role (clusterroles/roles) generated by the ruleId and check if it's in the list
	roleRef := getRoleRef(crb)
	
	roleNamespace := _get_namespace("get")
	roleRefKind := roleRef.kind
	# roleName := roleRef.name
	roleName := sprintf("%v.%v", [roleNamespace, roleRef.name])
	
	violationRoles = get_risky_role_rule_data(roleRefKind, ruleId)
	roleName  == violationRoles[_]`

	rego = append(rego, line)
	rego = append(rego, "}\n")

	////////////////////////////////////
	// start rego generation part 2B (for configuratoin assessment rbac data check)
	rego = append(rego, "# [part 2B] for configuratoin assessment session data check (rolebindings)")
	rego = append(rego, functionName)
	rego = append(rego, "{")
	rego = append(rego, fmt.Sprintf("	rulesToCheck := [%s]	# any rule met", arrayToString(ruleIDs, ",")))
	rego = append(rego, "	ruleId := rulesToCheck[_]")
	rego = append(rego, "	sa := request.spec.serviceAccountName")

	line = `
	# ==============================================
	# find corresponding role binding
	crb :=  data.neuvector.k8s.rolebindings[crb_name]

	subjects := getSubjects(crb)
    subject := subjects[i]

	subject.kind == "ServiceAccount"
	subject.name == sa`
	rego = append(rego, line)

	line = `
	# ==============================================
    # get corresponding role (clusterroles/roles) generated by the ruleId and check if it's in the list
	roleRef := getRoleRef(crb)
	
	roleNamespace := _get_namespace("get")
	roleRefKind := roleRef.kind
	# roleName := roleRef.name
	roleName := sprintf("%v.%v", [roleNamespace, roleRef.name])
	
	violationRoles = get_risky_role_rule_data(roleRefKind, ruleId)
	# roleName  == violationRoles[_]

	violationRoleName = violationRoles[_]
    contains(violationRoleName, "_config_assessment_")		# format signature: 12345_config_assessment_
	violationRoleName2 := substring(violationRoleName, 24, -1)
    roleName == violationRoleName2`

	rego = append(rego, line)
	rego = append(rego, "}\n")

	// foreach rule, we need to generate a pair of these code..
	for _, ruleID := range ruleIDs {
		rego = append(rego, "get_risky_role_rule_data(type, ruleId) := data2check {")
		rego = append(rego, `	type == "ClusterRole"`)
		rego = append(rego, fmt.Sprintf("	ruleId == %d", ruleID))
		rego = append(rego, fmt.Sprintf("	data2check = data.neuvector_policy_%d.violation_clusterroles", ruleID))
		rego = append(rego, "}")

		rego = append(rego, "get_risky_role_rule_data(type, ruleId) := data2check {")
		rego = append(rego, `	type == "Role"`)
		rego = append(rego, fmt.Sprintf("	ruleId == %d", ruleID))
		rego = append(rego, fmt.Sprintf("	data2check = data.neuvector_policy_%d.violation_roles", ruleID))
		rego = append(rego, "}")
	}

	return rego
}

func parseQuotedString(input string) []string {
	quotedString := []string{}

	s := strings.Split(input, ",")
	for _, v := range s {
		quotedString = append(quotedString, fmt.Sprintf("%q", strings.TrimSpace(v)))
	}

	return quotedString
}

func printSpec() string {

	rego := `
specification = spec {
	spec:={
		"version": "v1",
		"description": "NeuVector generated"
	}
}
	`
	return rego
}

func printHelperFunctions() string {
	rego := `
rawInput[msg]{
	msg := input
}

# type-1: for regular workload, get it's spec.template
_get_input(w) := x {
	w == "get"

	supportedKind = ["Deployment", "DaemonSet", "Job", "ReplicaSet", "ReplicationController", "StatefulSet"]
	input.request.kind.kind == supportedKind[_]

    input.request.object.spec.template	
    x := input.request.object.spec.template
}

# type-2: for cronjob workload, the format is different from major workload
_get_input(w) := x {
	w == "get"

	supportedKind = ["CronJob"]
	input.request.kind.kind == supportedKind[_]

	input.request.object.spec.jobTemplate.spec.template		
    x :=  input.request.object.spec.jobTemplate.spec.template
}

# type-3: for pod workload, the format is different from major workload
# We cannot use the [input.request.object] here because this will also generate output as [input.request.object.spec.template]
# this cause Rego runtime error
# 	"message": "functions must not produce multiple outputs for same inputs",
# need to add addition helper to check like kind==Pod
_get_input(w) := x {
	w == "get"
	input.request.kind.kind == "Pod"
	input.request.object
   x :=  input.request.object
}

# type-1a: [for testing in Rego Playground], same as type-1. comment out the false statement (1==2) for testing.
_get_input(w) := x {
	1==2

	w == "get"

	supportedKind = ["Deployment", "DaemonSet", "Job", "ReplicaSet", "ReplicationController", "StatefulSet"]
	input.input.request.kind.kind == supportedKind[_]

    input.input.request.object.spec.template		# used in Rego Playground (add prefix input)
    x := input.input.request.object.spec.template
}

# type-2a: [for testing in Rego Playground], same as type-2. comment out the false statement (1==2) for testing.
_get_input(w) := x {
	1==2

	w == "get"

	supportedKind = ["CronJob"]
	input.input.request.kind.kind == supportedKind[_]

	input.input.request.object.spec.jobTemplate.spec.template		
    x := input.input.request.object.spec.jobTemplate.spec.template
}

# type-3a: [for testing in Rego Playground], same as type-3. comment out the false statement (1==2) for testing.
_get_input(w) := x {
	1==2

	w == "get"
	input.input.request.kind.kind == "Pod"
	input.input.request.object
    x := input.input.request.object
}

_get_namespace(w) := x{
	w == "get"
	x := input.request.namespace
}

_get_namespace(w) := x{
	w == "get"
	not input.request.namespace
    x := "default"
}


getSubjects(binding):=subjects
{
	subjects := binding.request.object.subjects		# for data generated by admission review
}

getSubjects(binding):=subjects
{
	subjects := binding.subjects		# for data generated by xlate2()
}

getRoleRef(binding):=rolRef
{
    rolRef := binding.request.object.roleRef		# for data generated by admission review
}

getRoleRef(binding):=rolRef
{
    rolRef := binding.roleRef		# for data generated by xlate2()
}

## operator -- contains all (array)
operator_contains_all(criteria_values, items){
	is_array(items)
	matched := [name | items[i] == criteria_values[j]; name = items[i]]
	count(items) == count(matched)
}

## operator -- contains all (single value)
operator_contains_all(criteria_values, item){
	is_string(item)
	check_contains(criteria_values, item)
}

## operator -- contains any (array)
operator_contains_any(criteria_values, items){
	is_array(items)
	matched := [name | items[i] == criteria_values[j]; name = items[i]]
	count(matched)>=1
}

## operator -- contains any (single value)
operator_contains_any(criteria_values, item){
	is_string(item)
	check_contains(criteria_values, item)
}

## operator -- not contains any (array)
operator_not_contains_any(criteria_values, items){
	is_array(items)
	matched := [name | items[i] == criteria_values[j]; name = items[i]]
	count(matched)==0    
}

## operator -- not contains any (single value)
operator_not_contains_any(criteria_values, item){
	is_string(item)
	not check_contains(criteria_values, item)  
}

## operator -- contains other than  (array)
operator_contains_other_than(criteria_values, items){
	is_array(items)
	matched := [name | items[i] == criteria_values[j]; name = items[i]]
	count(items) != count(matched)
}

## operator -- contains other than (single value)
operator_contains_other_than(criteria_values, item){
	is_string(item)
	not check_contains(criteria_values, item)
}

check_contains(arrayData, elem) {
	arrayData[_] == elem
}

	`

	return rego
}

func printRiskyRoleFuntions() string {
	rego := `
rawInput[msg]{
	msg := input
}

getInput(w) := x {
	w == "formatA_admission_review"
	x := input.request	
}
	
getInput(w) := x {
	w == "formatB_config_assessment"
	x := input	
}

violation[result]{
	request := getInput("formatA_admission_review")
	
	oneRule := request.object.rules[_]
	
	# check [item.rules[_].resources] with [arrayContainsAny]
	array_contains_any(get_parameter_resources("get"), oneRule.resources)   
	
	# check [item.rules[_].verbs] with [arrayContainsAny]
	array_contains_any(get_parameter_verbs("get"), oneRule.verbs)    		

	result:={
		"message": "all criteria have been met (ar)"
	}
}

violation[result]{
	request := getInput("formatB_config_assessment")
	
	oneRule := request.object.rules[_]
	
	# check [item.rules[_].resources] with [arrayContainsAny]
	array_contains_any(get_parameter_resources("get"), oneRule.resources)   
	
	# check [item.rules[_].verbs] with [arrayContainsAny]
	array_contains_any(get_parameter_verbs("get"), oneRule.verbs)    		

	result:={
		"message": "all criteria have been met (config assessment)"
	}
}

#
# for [rule.UseAsRiskyRoleTag], the output will be used in
# 	violation_clusterroles[name]
# 	violation_roles[name]
#

# Risky ClusterRoles - process data generated from AdmissionReview
violation_clusterroles[name]{
	# crole := input.result.clusterroles[cr_name]				# in Rego Playground
	crole := data.neuvector.k8s.clusterroles[cr_name]		# in golang 
	oneRule := crole.request.object.rules[_]
	
	criteria_value_resources := get_parameter_resources("get")
	criteria_value_verbs := get_parameter_verbs("get")
	
	array_contains_any(criteria_value_resources, oneRule.resources)
	array_contains_any(criteria_value_verbs, oneRule.verbs)
	
	name := cr_name
}

# Risky ClusterRoles - process data generated from xlate2()
violation_clusterroles[name]{
	# crole := input.result.clusterroles[cr_name]
	crole := data.neuvector.k8s.clusterroles[cr_name]
	oneRule := crole.rules[_]

	criteria_value_resources := get_parameter_resources("get")
	criteria_value_verbs := get_parameter_verbs("get")
	array_contains_any(criteria_value_resources, oneRule.resources)
	array_contains_any(criteria_value_verbs, oneRule.verbs)

	name := cr_name
}

# Risky Roles - process data generated from AdmissionReview
violation_roles[name]{
	# role :=  input.result.roles[r_name]
	role := data.neuvector.k8s.roles[r_name]
	oneRule := role.request.object.rules[_]

	criteria_value_resources := get_parameter_resources("get")
	criteria_value_verbs := get_parameter_verbs("get")
	array_contains_any(criteria_value_resources, oneRule.resources)
	array_contains_any(criteria_value_verbs, oneRule.verbs)

	name := r_name
}

# Risky Roles - process data generated from xlate2()
violation_roles[name]{
	# role :=  input.result.roles[r_name]
	role := data.neuvector.k8s.roles[r_name]
	oneRule := role.rules[_]

	criteria_value_resources := get_parameter_resources("get")
	criteria_value_verbs := get_parameter_verbs("get")
	array_contains_any(criteria_value_resources, oneRule.resources)
	array_contains_any(criteria_value_verbs, oneRule.verbs)

	name := r_name
}

array_contains_any(arrayData, elemArray){
		elem := elemArray[_]
		arrayData[_] == elem
}
		
	`
	return rego
}

func FormatRiskyRuleMappingKey(ruleName string) string {
	return fmt.Sprintf("/v1/data/mapping/riskyroletags/%s", b64.StdEncoding.EncodeToString([]byte(ruleName)))
}

func AddRiskyRuleMapping(ruleName string, ruleId int) {
	mappingKey := FormatRiskyRuleMappingKey(ruleName)
	AddDocument(mappingKey, fmt.Sprintf(`{"ruleid": %d}`, ruleId))
}

func arrayToString(a []int, delim string) string {
	return strings.Trim(strings.Replace(fmt.Sprint(a), " ", delim, -1), "[]")
}
