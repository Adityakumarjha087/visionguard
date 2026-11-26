import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { incidentQuery } from "@/lib/api";
import type { IncidentFilters } from "@/lib/types";
import { camera, incident } from "@/test/fixtures";
import { IncidentDetail } from "../incidents/incident-detail";
import { IncidentFiltersBar, isoToLocal, localToIso } from "../incidents/incident-filters";
import { IncidentsTable } from "../incidents/incidents-table";
import { useState } from "react";

function Harness({ onQuery }: { onQuery: (qs: string) => void }) {
  const [f, setF] = useState<IncidentFilters>({ page: 4, page_size: 20 });
  return (
    <IncidentFiltersBar
      value={f}
      cameras={[camera()]}
      debounceMs={50}
      onChange={(next) => {
        setF(next);
        onQuery(incidentQuery(next));
      }}
    />
  );
}

describe("incident filters", () => {
  it("builds the query string from selects and resets to page 1", async () => {
    const onQuery = vi.fn();
    render(<Harness onQuery={onQuery} />);
    await userEvent.selectOptions(screen.getByLabelText("Severity"), "critical");
    expect(onQuery).toHaveBeenLastCalledWith("?severity=critical&page=1&page_size=20");
    await userEvent.selectOptions(screen.getByLabelText("Camera"), "c1");
    await userEvent.selectOptions(screen.getByLabelText("Status"), "open");
    await userEvent.selectOptions(screen.getByLabelText("Rule type"), "dwell_time");
    expect(onQuery).toHaveBeenLastCalledWith("?camera_id=c1&severity=critical&status=open&rule_type=dwell_time&page=1&page_size=20");
  });

  it("debounces the search box", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const onQuery = vi.fn();
    render(<Harness onQuery={onQuery} />);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await user.type(screen.getByLabelText("Search incidents"), "helmet");
    expect(onQuery).not.toHaveBeenCalledWith(expect.stringContaining("q=helm&"));
    await act(async () => {
      vi.advanceTimersByTime(60);
    });
    expect(onQuery).toHaveBeenLastCalledWith("?q=helmet&page=1&page_size=20");
    vi.useRealTimers();
  });

  it("clears all filters", async () => {
    const onQuery = vi.fn();
    render(<Harness onQuery={onQuery} />);
    await userEvent.selectOptions(screen.getByLabelText("Severity"), "low");
    await userEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(onQuery).toHaveBeenLastCalledWith("?page=1&page_size=20");
  });

  it("converts datetime-local values to ISO and back", () => {
    const iso = localToIso("2026-11-26T09:30")!;
    expect(iso).toMatch(/Z$/);
    expect(isoToLocal(iso)).toBe("2026-11-26T09:30");
    expect(localToIso("")).toBeUndefined();
  });
});

describe("IncidentsTable", () => {
  it("renders rows that open on click and keyboard", async () => {
    const onOpen = vi.fn();
    render(<IncidentsTable items={[incident(), incident({ id: "i2", severity: "low" })]} onOpen={onOpen} />);
    const rows = screen.getAllByTestId("incident-row");
    expect(rows).toHaveLength(2);
    await userEvent.click(rows[0]!);
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ id: "i1" }));
    rows[1]!.focus();
    await userEvent.keyboard("{Enter}");
    expect(onOpen).toHaveBeenLastCalledWith(expect.objectContaining({ id: "i2" }));
  });

  it("shows an empty state", () => {
    render(<IncidentsTable items={[]} onOpen={vi.fn()} />);
    expect(screen.getByText("No incidents")).toBeInTheDocument();
  });
});

describe("IncidentDetail", () => {
  it("shows description, provider badge and clip-processing placeholder", () => {
    render(<IncidentDetail incident={incident()} token="tok" canAct onStatus={vi.fn()} />);
    expect(screen.getByTestId("incident-description")).toHaveTextContent("restricted zone");
    expect(screen.getByTestId("description-provider")).toHaveTextContent("mock");
    expect(screen.getByTestId("clip-processing")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /Snapshot of incident/ }).getAttribute("src")).toContain("/api/v1/media/snapshots/i1.jpg?access_token=tok");
  });

  it("renders the clip video and pending description", () => {
    render(<IncidentDetail incident={incident({ clip_url: "/api/v1/media/clips/i1.mp4", description: null })} token="tok" canAct onStatus={vi.fn()} />);
    expect(screen.getByTestId("incident-clip")).toHaveAttribute("src", expect.stringContaining("/api/v1/media/clips/i1.mp4?access_token=tok"));
    expect(screen.getByTestId("description-pending")).toBeInTheDocument();
  });

  it("offers acknowledge/resolve only to operators", async () => {
    const onStatus = vi.fn().mockResolvedValue(undefined);
    const { rerender } = render(<IncidentDetail incident={incident()} token="t" canAct onStatus={onStatus} />);
    await userEvent.click(screen.getByRole("button", { name: /Acknowledge/ }));
    expect(onStatus).toHaveBeenCalledWith("acknowledged");
    rerender(<IncidentDetail incident={incident({ status: "acknowledged" })} token="t" canAct onStatus={onStatus} />);
    expect(screen.queryByRole("button", { name: /Acknowledge/ })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Resolve/ }));
    expect(onStatus).toHaveBeenLastCalledWith("resolved");
    rerender(<IncidentDetail incident={incident()} token="t" canAct={false} onStatus={onStatus} />);
    expect(screen.queryByRole("button", { name: /Resolve/ })).not.toBeInTheDocument();
  });
});
