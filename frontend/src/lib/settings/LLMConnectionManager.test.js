import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

beforeAll(() => {
	if (!Element.prototype.animate) {
		Element.prototype.animate = () => ({
			finished: Promise.resolve(),
			cancel: () => {},
			addEventListener: () => {},
			removeEventListener: () => {},
			play: () => {},
			pause: () => {},
		});
	}
});

vi.mock("../api.js", () => ({
	api: {
		llmConnections: {
			getAll: vi.fn(),
			test: vi.fn(),
		},
		llmProviders: {
			getProviders: vi.fn(),
		},
		actionCapabilities: {
			getAll: vi.fn(),
		},
	},
}));

vi.mock("../stores/toasts.svelte.js", () => ({
	errorToast: vi.fn(),
	successToast: vi.fn(),
}));

vi.mock("../stores/i18n.svelte.js", () => ({
	t: (key) => key,
}));

vi.mock("../composables/useConfirm.js", () => ({
	confirm: vi.fn(),
}));

import { api } from "../api.js";
import { errorToast } from "../stores/toasts.svelte.js";
import LLMConnectionManager from "./LLMConnectionManager.svelte";

const connection = {
	id: 7,
	name: "LLM proxy",
	provider_type: "local",
	model: "openai/gpt-5.3-chat-latest",
	base_url: "http://litellm:4000",
	provider_config: "",
	is_default: true,
	is_enabled: true,
};

describe("LLMConnectionManager connection test errors", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		api.llmConnections.getAll.mockResolvedValue([connection]);
		api.llmProviders.getProviders.mockResolvedValue([
			{
				type: "local",
				name: "Local / Custom",
				models: [{ id: connection.model, name: connection.model }],
			},
		]);
		api.actionCapabilities.getAll.mockResolvedValue([]);
	});

	it("shows only the provider message in an error toast and keeps it out of the modal", async () => {
		const providerMessage =
			"Could not finish the message because max_tokens was reached.";
		api.llmConnections.test.mockRejectedValue(
			new Error(
				`Connection test failed: failed to connect to LLM service: LLM API error: status 400 - ${JSON.stringify(
					{
						error: { message: providerMessage, type: "invalid_request_error" },
					},
				)}`,
			),
		);

		render(LLMConnectionManager);

		await fireEvent.click(await screen.findByTestId("llm-connection-edit"));
		await fireEvent.click(screen.getByTestId("llm-connection-test-modal"));

		await waitFor(() => {
			expect(errorToast).toHaveBeenCalledWith(
				providerMessage,
				"settings.adminOperations.llmConnections.testFailed",
			);
		});
		expect(screen.queryByText(providerMessage)).not.toBeInTheDocument();
		expect(screen.getByRole("dialog")).toBeInTheDocument();
	});
});
