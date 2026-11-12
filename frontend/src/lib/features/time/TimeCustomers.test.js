import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { writable } from "svelte/store";

vi.mock("../../api/configuration.js", () => ({
  customFields: { getAll: vi.fn() },
}));

vi.mock("../../api/portal.js", () => ({
  customerOrganisations: {
    getAll: vi.fn(),
    delete: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
  },
}));

vi.mock("../../stores/attachmentStatus.svelte.js", () => ({
  attachmentStatus: writable({ enabled: false }),
}));

vi.mock("../../stores/i18n.svelte.js", () => ({
  t: (key) =>
    ({
      "time.organizations.failedToDelete": "Failed to delete organization",
    })[key] ?? key,
}));

vi.mock("../../stores/toasts.svelte.js", () => ({
  surfaceError: vi.fn(),
  errorToast: vi.fn(),
}));

vi.mock("../../stores/permissions.svelte.js", () => ({
  permissionStore: writable({ userPermissionKeys: new Set(["customers.manage"]) }),
  isSystemAdmin: writable(false),
}));

vi.mock("../../composables/useConfirm.js", () => ({
  confirm: vi.fn(),
}));

import { customFields } from "../../api/configuration.js";
import { customerOrganisations } from "../../api/portal.js";
import { errorToast } from "../../stores/toasts.svelte.js";
import { confirm } from "../../composables/useConfirm.js";
import TimeCustomers from "./TimeCustomers.svelte";

describe("TimeCustomers delete errors", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    customFields.getAll.mockResolvedValue([]);
    customerOrganisations.getAll.mockResolvedValue([
      { id: 7, name: "Acme", active: true },
    ]);
  });

  it("surfaces a toast when deleting a linked organisation fails", async () => {
    confirm.mockResolvedValue(true);
    customerOrganisations.delete.mockRejectedValue(
      new Error("Cannot delete customer with associated projects"),
    );

    render(TimeCustomers);

    const trigger = await screen.findByTestId("time-customer-actions-7");
    await fireEvent.click(trigger);
    const deleteItem = await screen.findByTestId("time-customer-delete");
    await fireEvent.click(deleteItem);

    await waitFor(() => {
      expect(errorToast).toHaveBeenCalledWith(
        "Failed to delete organization: Cannot delete customer with associated projects",
      );
    });
  });

  it("does not call the API when the confirmation is dismissed", async () => {
    confirm.mockResolvedValue(false);

    render(TimeCustomers);

    const trigger = await screen.findByTestId("time-customer-actions-7");
    await fireEvent.click(trigger);
    const deleteItem = await screen.findByTestId("time-customer-delete");
    await fireEvent.click(deleteItem);

    await waitFor(() => {
      expect(confirm).toHaveBeenCalled();
    });
    expect(customerOrganisations.delete).not.toHaveBeenCalled();
    expect(errorToast).not.toHaveBeenCalled();
  });
});
