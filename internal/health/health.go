package health

import "worktracker/internal/model"

func Aggregate(parts []model.ComponentHealth) model.HealthLevel {
	level := model.Healthy
	for _, p := range parts {
		if p.Level == model.Failed {
			return model.Failed
		}
		if p.Level == model.Degraded {
			level = model.Degraded
		}
	}
	return level
}
