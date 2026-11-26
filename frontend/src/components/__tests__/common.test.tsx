import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api";
import { CameraStateBadge, IncidentStatusBadge, SeverityBadge } from "../common/badges";
import { EmptyState, ErrorState } from "../common/states";
import { ConnectionDot } from "../layout/connection-indicator";
import { AlertFeed } from "../dashboard/alert-feed";
import { KpiCards } from "../dashboard/kpi-cards";
import { incident } from "@/test/fixtures";

describe("badges", () => {
  it("renders severity with a per-level style", () => {
    const { rerender } = render(<SeverityBadge severity="critical" />);
    const b = screen.getByText("critical");
    expect(b).toHaveAttribute("data-severity", "critical");
    expect(b.className).toContain("bg-red-600");
    rerender(<SeverityBadge severity="low" />);
    expect(screen.getByText("low").className).toContain("sky");
  });
  it("shows fps only for online cameras", () => {
    const { rerender } = render(<CameraStateBadge state="online" fps={9.76} />);
    expect(screen.getByText(/9\.8 fps/)).toBeInTheDocument();
    rerender(<CameraStateBadge state="error" fps={0} />);
    expect(screen.queryByText(/fps/)).not.toBeInTheDocument();
    render(<IncidentStatusBadge status="resolved" />);
    expect(screen.getByText("resolved")).toBeInTheDocument();
  });
  it("labels the websocket status", () => {
    render(<ConnectionDot status="open" />);
    expect(screen.getByRole("status", { name: "Realtime connection: Live" })).toHaveAttribute("data-status", "open");
  });
});

describe("states", () => {
  it("renders an empty state with action", () => {
    render(<EmptyState title="Nothing here" description="Add something" action={<button>Add</button>} />);
    expect(screen.getByRole("status")).toHaveTextContent("Nothing here");
    expect(screen.getByRole("button", { name: "Add" })).toBeInTheDocument();
  });
  it("renders an error with retry", async () => {
    const onRetry = vi.fn();
    render(<ErrorState error={new ApiError(500, "internal", "database down")} onRetry={onRetry} />);
    expect(screen.getByRole("alert")).toHaveTextContent("database down");
    await userEvent.click(screen.getByRole("button", { name: /Retry/ }));
    expect(onRetry).toHaveBeenCalledOnce();
  });
});

describe("dashboard widgets", () => {
  it("renders the alert feed with snapshot thumbnails or an empty state", () => {
    const { rerender } = render(<AlertFeed items={[incident()]} token="tok" />);
    expect(screen.getAllByTestId("alert-item")).toHaveLength(1);
    expect(screen.getByRole("img").getAttribute("src")).toContain("access_token=tok");
    rerender(<AlertFeed items={[]} token="tok" />);
    expect(screen.getByText("No alerts yet")).toBeInTheDocument();
  });
  it("renders KPI cards", () => {
    render(
      <KpiCards
        s={{ cameras_online: 1, cameras_total: 2, open_incidents: 4, incidents_24h: 9, people_now: 3, vehicles_now: 1, crossings_in_24h: 5, crossings_out_24h: 2 }}
      />,
    );
    expect(screen.getByTestId("kpi-cameras-online")).toHaveTextContent("1/2");
    expect(screen.getByTestId("kpi-line-crossings-24h")).toHaveTextContent("7");
  });
});
