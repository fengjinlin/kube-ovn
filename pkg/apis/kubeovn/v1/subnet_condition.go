package v1

import (
	"encoding/json"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	SubnetReady ConditionType = "Ready"
)

const (
	SubnetReasonInit         = "Init"
	SubnetReasonValidateFail = "ValidateFail"
	SubnetReasonNonOvnSubnet = "NonOvnSubnet"
)

func (ss *SubnetStatus) ConditionBytes() ([]byte, error) {
	bytes, err := json.Marshal(ss.Conditions)
	if err != nil {
		return nil, err
	}
	newStr := fmt.Sprintf(`{"conditions": %s}`, string(bytes))
	return []byte(newStr), nil
}

func (ss *SubnetStatus) addCondition(cType ConditionType, status corev1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	c := &SubnetCondition{
		Type:               cType,
		LastUpdateTime:     now,
		LastTransitionTime: now,
		Status:             status,
		Reason:             reason,
		Message:            message,
	}
	ss.Conditions = append(ss.Conditions, *c)
}

// setConditionValue updates or creates a new condition
func (ss *SubnetStatus) setConditionValue(cType ConditionType, status corev1.ConditionStatus, reason, message string) {
	var c *SubnetCondition
	for i := range ss.Conditions {
		if ss.Conditions[i].Type == cType {
			c = &ss.Conditions[i]
		}
	}
	if c == nil {
		ss.addCondition(cType, status, reason, message)
	} else {
		// check message ?
		if c.Status == status && c.Reason == reason && c.Message == message {
			return
		}
		now := metav1.Now()
		c.LastUpdateTime = now
		if c.Status != status {
			c.LastTransitionTime = now
		}
		c.Status = status
		c.Reason = reason
		c.Message = message
	}
}

// RemoveCondition removes the condition with the provided type.
func (ss *SubnetStatus) RemoveCondition(cType ConditionType) {
	for i, c := range ss.Conditions {
		if c.Type == cType {
			ss.Conditions[i] = ss.Conditions[len(ss.Conditions)-1]
			ss.Conditions = ss.Conditions[:len(ss.Conditions)-1]
			break
		}
	}
}

// GetCondition get existing condition
func (ss *SubnetStatus) GetCondition(cType ConditionType) *SubnetCondition {
	for i := range ss.Conditions {
		if ss.Conditions[i].Type == cType {
			return &ss.Conditions[i]
		}
	}
	return nil
}

// IsReady returns true if ready condition is set
func (ss *SubnetStatus) IsReady() bool { return ss.isConditionTrue(SubnetReady) }

// IsConditionTrue - if condition is true
func (ss *SubnetStatus) isConditionTrue(cType ConditionType) bool {
	if c := ss.GetCondition(cType); c != nil {
		return c.Status == corev1.ConditionTrue
	}
	return false
}

// Ready - shortcut to set ready condition to true
func (ss *SubnetStatus) Ready(reason, message string) {
	ss.SetCondition(SubnetReady, reason, message)
}

// NotReady - shortcut to set ready condition to false
func (ss *SubnetStatus) NotReady(reason, message string) {
	ss.ClearCondition(SubnetReady, reason, message)
}

// EnsureCondition useful for adding default conditions
func (ss *SubnetStatus) EnsureCondition(cType ConditionType) {
	if c := ss.GetCondition(cType); c != nil {
		return
	}
	ss.addCondition(cType, corev1.ConditionUnknown, SubnetReasonInit, "Not Observed")
}

// EnsureStandardConditions - helper to inject standard conditions
func (ss *SubnetStatus) EnsureStandardConditions() {
	ss.EnsureCondition(SubnetReady)
}

// ClearCondition updates or creates a new condition
func (ss *SubnetStatus) ClearCondition(cType ConditionType, reason, message string) {
	ss.setConditionValue(cType, corev1.ConditionFalse, reason, message)
}

// SetCondition updates or creates a new condition
func (ss *SubnetStatus) SetCondition(cType ConditionType, reason, message string) {
	ss.setConditionValue(cType, corev1.ConditionTrue, reason, message)
}

// RemoveAllConditions updates or creates a new condition
func (ss *SubnetStatus) RemoveAllConditions() {
	ss.Conditions = []SubnetCondition{}
}

// ClearAllConditions updates or creates a new condition
func (ss *SubnetStatus) ClearAllConditions() {
	for i := range ss.Conditions {
		ss.Conditions[i].Status = corev1.ConditionFalse
	}
}
