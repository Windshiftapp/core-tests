import { beforeEach, describe, expect, test, vi } from "vitest";

vi.mock('../../api/channels.js', () => ({
  channels: {
  			getAll: vi.fn(),
  			update: vi.fn(),
  			updateConfig: vi.fn(),
  			toggle: vi.fn(),
  		},
  channelIntakes: {
  			list: vi.fn(),
  		},
}));

import { channelIntakes, channels } from '../../api/channels.js';
import { loadPortalConnectedMailboxes, parseChannelConfig, prepareFormChannelForWorkspace, saveChannelSettings } from "./channelAdmin.js";

beforeEach(() => {
	vi.clearAllMocks();
});

describe("parseChannelConfig", () => {
	test("uses an empty object for absent or blank config", () => {
		expect(parseChannelConfig(null)).toEqual({});
		expect(parseChannelConfig(undefined)).toEqual({});
		expect(parseChannelConfig("")).toEqual({});
		expect(parseChannelConfig("   ")).toEqual({});
	});

	test("accepts object config in either wire representation", () => {
		const config = { portal_slug: "support" };
		expect(parseChannelConfig(config)).toBe(config);
		expect(parseChannelConfig(JSON.stringify(config))).toEqual(config);
	});

	test.each([false, 0, [], "[1]", "null", "false"])(
		"rejects non-object config: %j",
		(config) => {
			expect(() => parseChannelConfig(config)).toThrow("Channel configuration");
		},
	);

	test("rejects malformed JSON", () => {
		expect(() => parseChannelConfig("{")).toThrow(
			"Channel configuration is invalid JSON",
		);
	});
});

describe("saveChannelSettings", () => {
	test("leaves channel status unchanged when the settings form has no enable control", async () => {
		await saveChannelSettings({
			channel: { id: 7, type: "form", direction: "inbound", status: "enabled" },
			channelFormData: { name: "Feedback", description: "", category_id: null },
			configRef: null,
		});

		expect(channels.update).toHaveBeenCalledOnce();
		expect(channels.toggle).not.toHaveBeenCalled();
	});
});

describe("loadPortalConnectedMailboxes", () => {
	test("includes mailboxes whose intake targets the portal", async () => {
		channels.getAll.mockResolvedValue([
			{ id: 1, name: "Support", config: "{}" },
			{ id: 2, name: "Sales", config: "{}" },
		]);
		channelIntakes.list.mockImplementation((channelId) =>
			Promise.resolve(
				channelId === 1
					? [{ portal_channel_id: 9, workspace_id: 2 }]
					: [{ portal_channel_id: null, workspace_id: 9 }],
			),
		);

		const linked = await loadPortalConnectedMailboxes(9);

		expect(linked.map((channel) => channel.id)).toEqual([1]);
	});

	test("ignores the legacy email_connected_portal_id key and consults intakes", async () => {
		channels.getAll.mockResolvedValue([
			{ id: 1, name: "Legacy", config: '{"email_connected_portal_id":9}' },
		]);
		channelIntakes.list.mockResolvedValue([]);

		const linked = await loadPortalConnectedMailboxes(9);

		expect(linked).toEqual([]);
		expect(channelIntakes.list).toHaveBeenCalledWith(1);
	});

	test("skips a mailbox whose intake list cannot be read", async () => {
		channels.getAll.mockResolvedValue([{ id: 1, name: "Support", config: "{}" }]);
		channelIntakes.list.mockRejectedValue(new Error("forbidden"));

		await expect(loadPortalConnectedMailboxes(9)).resolves.toEqual([]);
	});
});

describe("prepareFormChannelForWorkspace", () => {
	test("adds the form workspace and enables the channel before form creation", async () => {
		channels.updateConfig.mockResolvedValue({ success: true });
		channels.toggle.mockResolvedValue({ status: "enabled" });

		const result = await prepareFormChannelForWorkspace({
			channel: { id: 7, status: "disabled" },
			workspaceIds: [],
			workspaceId: 42,
		});

		expect(channels.updateConfig).toHaveBeenCalledWith(7, {
			form_workspace_ids: [42],
		});
		expect(channels.toggle).toHaveBeenCalledWith(7);
		expect(result).toEqual({ workspaceIds: [42], status: "enabled" });
	});

	test("preserves an enabled channel that already serves the workspace", async () => {
		const workspaceIds = [42];

		const result = await prepareFormChannelForWorkspace({
			channel: { id: 7, status: "enabled" },
			workspaceIds,
			workspaceId: 42,
		});

		expect(channels.updateConfig).not.toHaveBeenCalled();
		expect(channels.toggle).not.toHaveBeenCalled();
		expect(result).toEqual({ workspaceIds, status: "enabled" });
	});
});
