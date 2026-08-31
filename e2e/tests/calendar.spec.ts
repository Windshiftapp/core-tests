import { expect, test } from '../fixtures/context-path';
import { CalendarPage } from '../pages/calendar.page';

/**
 * Weekly Calendar View Tests
 *
 * Calendar lives at `/personal/calendar` in the product — it is reached from
 * the personal-workspace sidebar (`WorkspaceNavigation.svelte:401`) and from
 * `PlanMyDay.svelte:253`. The `/workspaces/:id/calendar` route exists in the
 * router but no UI surface links to it; an earlier version of this spec
 * created a regular workspace and exercised the workspace-scoped URL, which
 * meant it ran against a code path real users never touch.
 */

test.describe('Calendar View', () => {
  let calendarPage: CalendarPage;

  test.beforeEach(async ({ page }) => {
    calendarPage = new CalendarPage(page);
  });

  test.describe('Calendar Display', () => {
    test('should display calendar view', async () => {
      await calendarPage.goto();
      await calendarPage.verifyCalendarVisible();
    });

    test('should display current week range', async () => {
      await calendarPage.goto();

      const weekRange = await calendarPage.getCurrentWeekRange();
      expect(weekRange.length).toBeGreaterThan(0);
    });
  });

  test.describe('Calendar Navigation', () => {
    test('should navigate to previous week', async () => {
      await calendarPage.goto();

      const currentWeek = await calendarPage.getCurrentWeekRange();
      await calendarPage.goToPreviousWeek();
      const previousWeek = await calendarPage.getCurrentWeekRange();

      expect(previousWeek).not.toEqual(currentWeek);
    });

    test('should navigate to next week', async () => {
      await calendarPage.goto();

      const currentWeek = await calendarPage.getCurrentWeekRange();
      await calendarPage.goToNextWeek();
      const nextWeek = await calendarPage.getCurrentWeekRange();

      expect(nextWeek).not.toEqual(currentWeek);
    });

    test('should navigate back to this week', async () => {
      await calendarPage.goto();

      const currentWeek = await calendarPage.getCurrentWeekRange();

      // Navigate away
      await calendarPage.goToPreviousWeek();
      await calendarPage.goToPreviousWeek();

      // Navigate back to this week
      await calendarPage.goToThisWeek();
      const thisWeek = await calendarPage.getCurrentWeekRange();

      expect(thisWeek).toEqual(currentWeek);
    });
  });
});
