import { render, screen } from "@testing-library/svelte";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../stores/i18n.svelte.js", () => ({
	t: (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key),
}));

import Chart from "./Chart.svelte";

beforeEach(() => {
	globalThis.ResizeObserver = class {
		observe() {}
		unobserve() {}
		disconnect() {}
	};
});

describe("Chart line interpolation", () => {
	it("draws a continuous cubic curve without the old quadratic joins", () => {
		render(Chart, {
			props: {
				type: "line",
				categories: ["One", "Two", "Three", "Four"],
				series: [
					{
						key: "completed",
						label: "Completed",
						color: "#22a06b",
						values: [2, 9, 4, 8],
						showArea: false,
					},
				],
			},
		});

		const path = screen.getByTestId("chart-series-completed").getAttribute("d");
		expect(path).toMatch(/^M [\d.]+ [\d.]+ C /);
		expect(path?.match(/ C /g)).toHaveLength(3);
		expect(path).not.toContain(" Q ");
	});
});

describe("Chart pie", () => {
	function renderPie(props) {
		return render(Chart, {
			props: {
				type: "pie",
				slices: [
					{ label: "Open", value: 6 },
					{ label: "In progress", value: 3 },
					{ label: "Done", value: 1 },
				],
				...props,
			},
		});
	}

	it("draws one wedge per slice and labels the slices with their share", () => {
		renderPie();

		expect(screen.getByTestId("chart-slice-0")).toBeInTheDocument();
		expect(screen.getByTestId("chart-slice-2")).toBeInTheDocument();
		expect(screen.queryByTestId("chart-slice-3")).not.toBeInTheDocument();

		// 60%, 30%, 10% of the total.
		expect(screen.getByTestId("chart-slice-label-0")).toHaveTextContent("60%");
		expect(screen.getByTestId("chart-slice-label-1")).toHaveTextContent("30%");
		expect(screen.getByTestId("chart-slice-label-2")).toHaveTextContent("10%");
	});

	it("lists every slice in the legend so small slices stay readable", () => {
		renderPie();

		const legend = screen.getByTestId("chart-pie-legend");
		expect(legend).toHaveTextContent("Open");
		expect(legend).toHaveTextContent("In progress");
		expect(legend).toHaveTextContent("Done");
		expect(screen.getByTestId("chart-slice-legend-2")).toHaveTextContent("10%");
	});

	it("collapses the tail beyond maxSlices into an Other slice", () => {
		render(
			Chart,
			{
				props: {
					type: "pie",
					maxSlices: 3,
					slices: [
						{ label: "A", value: 50 },
						{ label: "B", value: 30 },
						{ label: "C", value: 10 },
						{ label: "D", value: 6 },
						{ label: "E", value: 4 },
					],
				},
			},
		);

		const legend = screen.getByTestId("chart-pie-legend");
		expect(legend).toHaveTextContent("A");
		expect(legend).toHaveTextContent("B");
		expect(legend).toHaveTextContent('widgets.chart.otherSlices:{"count":3}');
		expect(legend).not.toHaveTextContent("C");
		// C, D, and E are 10, 6, and 4 of the total 100, so Other carries 20%.
		expect(screen.getByTestId("chart-slice-legend-2")).toHaveTextContent("20%");
	});

	it("draws a single full-circle slice without a degenerate arc", () => {
		render(Chart, { props: { type: "pie", slices: [{ label: "Only", value: 4 }] } });

		const path = screen.getByTestId("chart-slice-0").getAttribute("d");
		expect(path?.match(/ A /g)).toHaveLength(2);
		expect(screen.getByTestId("chart-slice-label-0")).toHaveTextContent("100%");
	});

	it("shows the empty message when every slice is zero", () => {
		render(Chart, {
			props: { type: "pie", emptyMessage: "Nothing to show", slices: [{ label: "A", value: 0 }] },
		});

		expect(screen.queryByTestId("chart-slice-0")).not.toBeInTheDocument();
		expect(screen.getByText("Nothing to show")).toBeInTheDocument();
	});

	it("draws no axis grid lines or category labels, which a pie does not have", () => {
		const { container } = renderPie();

		// The pie's own slice label is the only text inside the svg; grid lines
		// would otherwise draw at the top and bottom of the plot area.
		const svg = container.querySelector("svg");
		expect(svg.querySelectorAll("line")).toHaveLength(0);
		const svgText = [...svg.querySelectorAll("text")].map((node) => node.textContent.trim());
		expect(svgText).toEqual(["60%", "30%", "10%"]);
	});
});
