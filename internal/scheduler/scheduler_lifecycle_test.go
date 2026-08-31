package scheduler

import (
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/plugins"
)

type noopSMTPSender struct{}

func (noopSMTPSender) SendBatchedNotifications(string, string, []models.Notification) error {
	return nil
}

func (noopSMTPSender) IsSMTPConfigured() bool {
	return false
}

func TestNotificationSchedulerCanRestartAfterStop(t *testing.T) {
	scheduler := NewNotificationScheduler(nil, noopSMTPSender{}, 0, nil)
	if scheduler.ticker != nil {
		t.Fatal("constructor should not start the ticker before Start")
	}

	scheduler.Start()
	scheduler.Stop()
	if scheduler.ticker != nil {
		t.Fatal("Stop should clear the stopped ticker")
	}

	scheduler.Start()
	scheduler.Stop()
}

type noopPluginScheduleInvoker struct{}

func (noopPluginScheduleInvoker) DueSchedules(time.Time) []plugins.DueSchedule {
	return nil
}

func (noopPluginScheduleInvoker) CallPluginFunction(string, string, any) ([]byte, error) {
	return nil, nil
}

func TestPluginScheduleSchedulerCanRestartAfterStop(t *testing.T) {
	scheduler := NewPluginScheduleSchedulerWithInterval(noopPluginScheduleInvoker{}, nil, time.Hour)
	scheduler.runRepo = nil

	scheduler.Start()
	scheduler.Stop()

	scheduler.Start()
	scheduler.Stop()
}
