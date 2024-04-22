package api

import (
	"bytes"
	"fmt"
)

// GetConditionByType gets condition by type.
func GetConditionByType(conditionType ClusterAutoscalerConditionType,
	conditions []ClusterAutoscalerCondition) *ClusterAutoscalerCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func getConditionsString(autoscalerConditions []ClusterAutoscalerCondition, prefix string) string {
	health := fmt.Sprintf("%v%-12v <unknown>", prefix, ClusterAutoscalerHealth+":")
	var scaleUp, scaleDown string
	var buffer, other bytes.Buffer
	for _, condition := range autoscalerConditions {
		var line bytes.Buffer
		line.WriteString(fmt.Sprintf("%v%-12v %v",
			prefix,
			condition.Type+":",
			condition.Status))
		if condition.Message != "" {
			line.WriteString(" (")
			line.WriteString(condition.Message)
			line.WriteString(")")
		}
		line.WriteString("\n")
		line.WriteString(fmt.Sprintf("%v%13sLastProbeTime:      %v\n",
			prefix,
			"",
			condition.LastProbeTime))
		line.WriteString(fmt.Sprintf("%v%13sLastTransitionTime: %v\n",
			prefix,
			"",
			condition.LastTransitionTime))
		switch condition.Type {
		case ClusterAutoscalerHealth:
			health = line.String()
		case ClusterAutoscalerScaleUp:
			scaleUp = line.String()
		case ClusterAutoscalerScaleDown:
			scaleDown = line.String()
		default:
			other.WriteString(line.String())
		}
	}
	buffer.WriteString(health)
	buffer.WriteString(scaleUp)
	buffer.WriteString(scaleDown)
	buffer.WriteString(other.String())
	return buffer.String()
}

// GetReadableString produces human-readable description of status.
func (status ClusterAutoscalerStatus) GetReadableString() string {
	var buffer bytes.Buffer
	buffer.WriteString("Cluster-wide:\n")
	buffer.WriteString(getConditionsString(status.ClusterwideConditions, "  "))
	if len(status.NodeGroupStatuses) == 0 {
		return buffer.String()
	}
	buffer.WriteString("\nNodeGroups:\n")
	for _, nodeGroupStatus := range status.NodeGroupStatuses {
		// buffer.WriteString(fmt.Sprintf("  Name:        %v\n", nodeGroupStatus.ProviderID))
		buffer.WriteString(getConditionsString(nodeGroupStatus.Conditions, "  "))
		buffer.WriteString("\n")
	}
	return buffer.String()
}