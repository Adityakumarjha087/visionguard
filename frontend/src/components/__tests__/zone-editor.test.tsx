import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { zone } from "@/test/fixtures";
import { ZoneEditor } from "../cameras/zone-editor";

function mockCanvasRect() {
  const canvas = screen.getByTestId("zone-canvas");
  canvas.getBoundingClientRect = () => ({ left: 10, top: 20, width: 200, height: 100, x: 10, y: 20, right: 210, bottom: 120, toJSON: () => ({}) });
  return canvas;
}

describe("ZoneEditor", () => {
  beforeEach(() => vi.clearAllMocks());

  it("adds normalised polygon vertices on click and saves", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ZoneEditor imageSrc={null} zones={[]} canEdit onSave={onSave} onDelete={vi.fn()} />);
    const canvas = mockCanvasRect();
    fireEvent.click(canvas, { clientX: 10, clientY: 20, detail: 1 });
    fireEvent.click(canvas, { clientX: 110, clientY: 20, detail: 1 });
    fireEvent.click(canvas, { clientX: 110, clientY: 70, detail: 1 });
    fireEvent.click(canvas, { clientX: 110, clientY: 70, detail: 2 }); // dbl-click 2nd click ignored
    expect(screen.getByTestId("draft-points")).toHaveTextContent("3 points");
    fireEvent.doubleClick(canvas);
    expect(screen.getByText("closed")).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText("Name"), "Dock");
    await userEvent.click(screen.getByRole("button", { name: /Save zone/ }));
    expect(onSave).toHaveBeenCalledWith({
      name: "Dock",
      kind: "polygon",
      points: [
        [0, 0],
        [0.5, 0],
        [0.5, 0.5],
      ],
      color: "#22c55e",
    });
    expect(screen.getByTestId("draft-points")).toHaveTextContent("0 points");
  });

  it("limits a line to two points and rejects saving an incomplete polygon", async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<ZoneEditor imageSrc={null} zones={[]} canEdit onSave={onSave} onDelete={vi.fn()} />);
    const canvas = mockCanvasRect();
    await userEvent.type(screen.getByLabelText("Name"), "Gate");
    fireEvent.click(canvas, { clientX: 60, clientY: 70, detail: 1 });
    await userEvent.click(screen.getByRole("button", { name: /Save zone/ }));
    expect(screen.getByRole("alert")).toHaveTextContent("at least 3 points");

    await userEvent.selectOptions(screen.getByLabelText("Kind"), "line");
    expect(screen.getByTestId("draft-points")).toHaveTextContent("0 points");
    fireEvent.click(canvas, { clientX: 10, clientY: 70, detail: 1 });
    fireEvent.click(canvas, { clientX: 210, clientY: 70, detail: 1 });
    fireEvent.click(canvas, { clientX: 100, clientY: 100, detail: 1 });
    expect(screen.getByTestId("draft-points")).toHaveTextContent("2 points");
    await userEvent.click(screen.getByRole("button", { name: /Save zone/ }));
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ kind: "line", points: [[0, 0.5], [1, 0.5]] }));
  });

  it("undo removes the last vertex", () => {
    render(<ZoneEditor imageSrc={null} zones={[]} canEdit onSave={vi.fn()} onDelete={vi.fn()} />);
    const canvas = mockCanvasRect();
    fireEvent.click(canvas, { clientX: 20, clientY: 30, detail: 1 });
    fireEvent.click(canvas, { clientX: 40, clientY: 30, detail: 1 });
    fireEvent.click(screen.getByRole("button", { name: /Undo/ }));
    expect(screen.getByTestId("draft-points")).toHaveTextContent("1 point");
  });

  it("is read-only for viewers but lists zones", () => {
    render(<ZoneEditor imageSrc={null} zones={[zone()]} canEdit={false} onSave={vi.fn()} onDelete={vi.fn()} />);
    const canvas = mockCanvasRect();
    fireEvent.click(canvas, { clientX: 50, clientY: 50, detail: 1 });
    expect(screen.getByTestId("draft-points")).toHaveTextContent("0 points");
    expect(screen.queryByRole("button", { name: /Save zone/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Delete zone/ })).not.toBeInTheDocument();
    expect(screen.getAllByTestId("zone-item")).toHaveLength(1);
  });
});
