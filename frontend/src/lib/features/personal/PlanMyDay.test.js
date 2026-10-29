import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock('../../api/ai.js', () => ({
  ai: {
  			planMyDay: vi.fn(),
  		},
  llmProviders: {
  			getEnabled: vi.fn(),
  		},
}));

vi.mock("../../router.js", () => ({
	navigate: vi.fn(),
}));

vi.mock('../../stores/auth.svelte.js', () => ({
  authStore: {
  		currentUser: null,
  	},
}));

import { ai, llmProviders } from '../../api/ai.js';
import PlanMyDay from "./PlanMyDay.svelte";

describe("PlanMyDay", () => {
	beforeEach(() => {
		llmProviders.getEnabled.mockResolvedValue(null);
		ai.planMyDay.mockResolvedValue({
			summary: "",
			activities: [],
		});
	});

	it("supports an empty provider list without crashing", async () => {
		render(PlanMyDay);

		await waitFor(() => {
			expect(llmProviders.getEnabled).toHaveBeenCalledOnce();
		});
		const generateButton = screen.getByTestId("plan-my-day-generate");
		expect(generateButton).toBeInTheDocument();

		await fireEvent.click(generateButton);

		await waitFor(() => {
			expect(ai.planMyDay).toHaveBeenCalledWith(null);
		});
		expect(screen.getByTestId("plan-my-day-view")).toBeInTheDocument();
	});
});
