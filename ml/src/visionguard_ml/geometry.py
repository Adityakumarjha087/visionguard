"""Pure 2D geometry helpers (normalised coordinates)."""

from __future__ import annotations

from collections.abc import Sequence

from visionguard_ml.types import Point


def point_in_polygon(pt: Point, polygon: Sequence[Point]) -> bool:
    """Ray-casting point-in-polygon test. Points on an edge may fall either way."""
    x, y = pt
    inside = False
    n = len(polygon)
    if n < 3 or is_collinear(polygon):
        return False  # degenerate (collinear / zero-area) zones never match
    j = n - 1
    for i in range(n):
        xi, yi = polygon[i]
        xj, yj = polygon[j]
        if (yi > y) != (yj > y):
            x_cross = (xj - xi) * (y - yi) / (yj - yi) + xi
            if x < x_cross:
                inside = not inside
        j = i
    return inside


def polygon_area(polygon: Sequence[Point]) -> float:
    """Signed shoelace area (self-intersecting shapes may partially cancel)."""
    n = len(polygon)
    return 0.5 * sum(
        polygon[i][0] * polygon[(i + 1) % n][1] - polygon[(i + 1) % n][0] * polygon[i][1] for i in range(n)
    )


def is_collinear(points: Sequence[Point], eps: float = 1e-9) -> bool:
    """True when all points lie on one line (a zero-area, degenerate polygon)."""
    base = points[0]
    far = max(points, key=lambda p: (p[0] - base[0]) ** 2 + (p[1] - base[1]) ** 2)
    if far == base:
        return True
    return all(abs(cross(base, far, p)) < eps for p in points)


def cross(o: Point, a: Point, b: Point) -> float:
    """Z component of (a - o) x (b - o)."""
    return (a[0] - o[0]) * (b[1] - o[1]) - (a[1] - o[1]) * (b[0] - o[0])


def side_of_line(pt: Point, p0: Point, p1: Point) -> float:
    """Signed side of ``pt`` relative to the directed line p0->p1 (image coordinates, y down).

    Negative means the point is on the visual *left* of the direction of travel.
    """
    return cross(p0, p1, pt)


def segments_intersect(a0: Point, a1: Point, b0: Point, b1: Point) -> bool:
    """Proper or touching intersection of segments a0-a1 and b0-b1."""
    d1 = cross(b0, b1, a0)
    d2 = cross(b0, b1, a1)
    d3 = cross(a0, a1, b0)
    d4 = cross(a0, a1, b1)
    if ((d1 > 0 > d2) or (d1 < 0 < d2)) and ((d3 > 0 > d4) or (d3 < 0 < d4)):
        return True
    return (
        (d1 == 0 and _on_segment(b0, b1, a0))
        or (d2 == 0 and _on_segment(b0, b1, a1))
        or (d3 == 0 and _on_segment(a0, a1, b0))
        or (d4 == 0 and _on_segment(a0, a1, b1))
    )


def _on_segment(p: Point, q: Point, r: Point) -> bool:
    return min(p[0], q[0]) <= r[0] <= max(p[0], q[0]) and min(p[1], q[1]) <= r[1] <= max(p[1], q[1])


def iou_matrix(
    a: Sequence[tuple[float, float, float, float]], b: Sequence[tuple[float, float, float, float]]
) -> list[list[float]]:
    """Pairwise IoU between two lists of xyxy boxes (pure python; numpy version lives in tracking)."""
    out: list[list[float]] = []
    for ax1, ay1, ax2, ay2 in a:
        row: list[float] = []
        area_a = max(0.0, ax2 - ax1) * max(0.0, ay2 - ay1)
        for bx1, by1, bx2, by2 in b:
            iw = max(0.0, min(ax2, bx2) - max(ax1, bx1))
            ih = max(0.0, min(ay2, by2) - max(ay1, by1))
            inter = iw * ih
            area_b = max(0.0, bx2 - bx1) * max(0.0, by2 - by1)
            union = area_a + area_b - inter
            row.append(inter / union if union > 0 else 0.0)
        out.append(row)
    return out
