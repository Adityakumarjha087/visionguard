import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { zone } from "@/test/fixtures";
import { RuleForm } from "../cameras/rule-form";
import type { Rule } from "@/lib/types";

const zones = [zone({ id: "p1", name: "Dock" }), zone({ id: "l1", name: "Gate line", kind: "line", points: [[0, 0.5], [1, 0.5]] })];

describe("RuleForm", () => {
  it("shows classes for zone_intrusion and only polygon zones", () => {
    render(<RuleForm zones={zones} onSubmit={vi.fn()} />);
    expect(screen.getByLabelText("Classes")).toHaveValue("person");
    expect(screen.queryByLabelText("Stationary seconds")).not.toBeInTheDocument();
    const zoneSel = screen.getByLabelText("Zone (polygon)");
    expect(Array.from((zoneSel as HTMLSelectElement).options).map((o) => o.text)).toEqual(["Select a zone…", "Dock"]);
  });

  it("switching to dwell_time shows seconds (default 120) and vehicle classes", async () => {
    render(<RuleForm zones={zones} onSubmit={vi.fn()} />);
    await userEvent.selectOptions(screen.getByLabelText("Type"), "dwell_time");
    expect(screen.getByLabelText("Stationary seconds")).toHaveValue(120);
    expect(screen.getByLabelText("Classes")).toHaveValue("car, truck, bus, motorcycle, vehicle");
  });

  it("crowd_count has optional zone + max_count; line_crossing only lists line zones", async () => {
    render(<RuleForm zones={zones} onSubmit={vi.fn()} />);
    await userEvent.selectOptions(screen.getByLabelText("Type"), "crowd_count");
    expect(screen.getByLabelText(/Max people/)).toBeInTheDocument();
    expect(screen.getByLabelText("Zone (polygon, optional)")).toHaveValue("");
    expect(screen.queryByLabelText("Classes")).not.toBeInTheDocument();
    await userEvent.selectOptions(screen.getByLabelText("Type"), "line_crossing");
    const sel = screen.getByLabelText("Zone (line)") as HTMLSelectElement;
    expect(sel.value).toBe("l1");
    expect(screen.queryByLabelText(/Max people/)).not.toBeInTheDocument();
  });

  it("missing_ppe shows grace seconds", async () => {
    render(<RuleForm zones={zones} onSubmit={vi.fn()} />);
    await userEvent.selectOptions(screen.getByLabelText("Type"), "missing_ppe");
    expect(screen.getByLabelText("Grace seconds")).toBeInTheDocument();
    expect(screen.getByLabelText("Zone (polygon, optional)")).toBeInTheDocument();
  });

  it("validates and submits a sanitised payload", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<RuleForm zones={zones} onSubmit={onSubmit} />);
    await userEvent.click(screen.getByRole("button", { name: "Create rule" }));
    expect(screen.getByText("Name is required")).toBeInTheDocument();
    expect(screen.getByText("Select a polygon zone")).toBeInTheDocument();
    expect(onSubmit).not.toHaveBeenCalled();

    await userEvent.type(screen.getByLabelText("Name"), "Parked trucks");
    await userEvent.selectOptions(screen.getByLabelText("Type"), "dwell_time");
    await userEvent.selectOptions(screen.getByLabelText("Zone (polygon)"), "p1");
    await userEvent.clear(screen.getByLabelText("Stationary seconds"));
    await userEvent.type(screen.getByLabelText("Stationary seconds"), "90");
    await userEvent.selectOptions(screen.getByLabelText("Severity"), "high");
    await userEvent.click(screen.getByLabelText("Notify by email"));
    await userEvent.click(screen.getByRole("button", { name: "Create rule" }));
    expect(onSubmit).toHaveBeenCalledWith({
      name: "Parked trucks",
      type: "dwell_time",
      zone_id: "p1",
      params: { seconds: 90, classes: ["car", "truck", "bus", "motorcycle", "vehicle"], cooldown_seconds: 30 },
      severity: "high",
      enabled: true,
      notify_email: true,
      notify_telegram: false,
    });
  });

  it("pre-fills when editing an existing rule", () => {
    const rule: Rule = {
      id: "r1",
      camera_id: "c1",
      zone_id: null,
      name: "Crowd",
      type: "crowd_count",
      params: { max_count: 12 },
      severity: "critical",
      enabled: false,
      notify_email: false,
      notify_telegram: true,
      created_at: "2026-11-26T00:00:00Z",
    };
    render(<RuleForm zones={zones} initial={rule} onSubmit={vi.fn()} />);
    expect(screen.getByLabelText("Name")).toHaveValue("Crowd");
    expect(screen.getByLabelText(/Max people/)).toHaveValue(12);
    expect(screen.getByLabelText("Notify via Telegram")).toBeChecked();
    expect(screen.getByRole("button", { name: "Save rule" })).toBeInTheDocument();
  });
});
