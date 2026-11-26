"""Deterministic synthetic demo footage.

Real CCTV footage is large and carries licensing/privacy baggage, so VisionGuard ships a
scripted scene renderer instead. Actors are drawn with distinctive colours that the
``color-synthetic`` detector understands, which makes every rule (intrusion, dwell, PPE,
crowd, line crossing) reproducible in tests and in the demo stack.

Colour contract (BGR) shared with :mod:`visionguard_ml.detectors.color`:
  person body  -> saturated red, head -> skin tone (or yellow helmet)
  vehicle      -> saturated blue body with dark windows
"""

from __future__ import annotations

import argparse
import logging
from collections.abc import Sequence
from dataclasses import dataclass
from itertools import pairwise
from pathlib import Path
from typing import cast

import cv2
import numpy as np

from visionguard_ml.types import Frame

log = logging.getLogger(__name__)

WIDTH, HEIGHT, FPS = 640, 360, 10
PERSON_BODY = (40, 40, 210)
SKIN = (140, 170, 210)
HELMET = (0, 215, 255)
VEHICLE = (200, 90, 30)
WINDOW = (70, 60, 50)
BODY_W, BODY_H, HEAD_R = 22, 46, 8
CAR_W, CAR_H = 84, 40

Keyframe = tuple[float, float, float]  # (t seconds, x px centre, y px feet)


@dataclass(frozen=True)
class Actor:
    kind: str  # "person" | "vehicle"
    path: tuple[Keyframe, ...]
    helmet: bool = True

    def position(self, t: float) -> tuple[float, float] | None:
        p = self.path
        if t < p[0][0] or t > p[-1][0]:
            return None
        for (t0, x0, y0), (t1, x1, y1) in pairwise(p):
            if t0 <= t <= t1:
                a = 0.0 if t1 == t0 else (t - t0) / (t1 - t0)
                return (x0 + (x1 - x0) * a, y0 + (y1 - y0) * a)
        return (p[-1][1], p[-1][2])


@dataclass(frozen=True)
class Scene:
    name: str
    duration: float
    actors: tuple[Actor, ...]
    seed: int = 7


def _p(*kf: Keyframe) -> tuple[Keyframe, ...]:
    return tuple(kf)


# Normalised zones used by the seed script match these coordinates:
#   restricted  x .70-.95 y .10-.45 | loading bay x .04-.40 y .58-.97
#   assembly    x .40-.66 y .40-.75 | counting line x = .5
WAREHOUSE = Scene(
    name="warehouse",
    duration=60.0,
    actors=(
        # Intruder: walks into the restricted zone and lingers.
        Actor("person", _p((0, 560, 340), (6, 560, 220), (9, 520, 130), (18, 540, 120), (24, 600, 345))),
        # Worker crossing the counting line left -> right.
        Actor("person", _p((1, 30, 250), (19, 610, 250))),
        # Worker WITHOUT helmet crossing right -> left.
        Actor("person", _p((14, 620, 300), (34, 40, 300)), helmet=False),
        # Crowd forming in the assembly area.
        Actor("person", _p((24, 300, 355), (30, 300, 230), (46, 300, 230), (52, 200, 355))),
        Actor("person", _p((24, 330, 355), (30, 340, 260), (46, 340, 260), (52, 420, 355))),
        Actor("person", _p((25, 380, 355), (31, 380, 230), (46, 380, 230), (52, 470, 355))),
        Actor("person", _p((25, 420, 355), (31, 412, 262), (46, 412, 262), (53, 520, 355))),
        Actor("person", _p((26, 270, 140), (31, 270, 200), (46, 270, 200), (53, 270, 355))),
        # Late walker right -> left.
        Actor("person", _p((44, 630, 170), (58, 20, 170))),
        # Truck enters, parks in the loading bay (stationary ~33 s), leaves.
        Actor("vehicle", _p((2, -50, 310), (9, 130, 310), (42, 130, 310), (50, -60, 310))),
        # Vehicle passing along the top road right -> left.
        Actor("vehicle", _p((18, 700, 60), (34, -60, 60))),
    ),
)

STREET = Scene(
    name="street",
    duration=40.0,
    seed=11,
    actors=(
        Actor("vehicle", _p((0, -60, 120), (8, 700, 120))),
        Actor("vehicle", _p((6, 700, 200), (15, -60, 200))),
        Actor("vehicle", _p((14, -60, 120), (20, 300, 120), (34, 300, 120), (40, 700, 120))),
        Actor("person", _p((0, 40, 320), (20, 600, 320))),
        Actor("person", _p((10, 620, 345), (32, 30, 345)), helmet=False),
        Actor("person", _p((22, 100, 300), (38, 560, 300))),
    ),
)

SCENES: dict[str, Scene] = {s.name: s for s in (WAREHOUSE, STREET)}


def _background(seed: int) -> Frame:
    rng = np.random.default_rng(seed)
    bg = np.full((HEIGHT, WIDTH, 3), 62, dtype=np.uint8)
    for x in range(0, WIDTH, 40):
        cv2.line(bg, (x, 0), (x, HEIGHT), (78, 78, 78), 1)
    for y in range(0, HEIGHT, 40):
        cv2.line(bg, (0, y), (WIDTH, y), (78, 78, 78), 1)
    cv2.rectangle(bg, (0, 30), (WIDTH, 90), (48, 48, 48), -1)  # road
    for x in range(10, WIDTH, 60):
        cv2.line(bg, (x, 60), (x + 30, 60), (190, 190, 190), 2)
    noise = rng.integers(-6, 7, size=bg.shape, dtype=np.int16)
    return cast(Frame, np.clip(bg.astype(np.int16) + noise, 0, 255).astype(np.uint8))


def _draw_person(img: Frame, x: float, feet: float, helmet: bool) -> None:
    x1, y2 = int(x - BODY_W / 2), int(feet)
    y1 = y2 - BODY_H
    cv2.rectangle(img, (x1, y1), (x1 + BODY_W, y2), PERSON_BODY, -1)
    head_c = (int(x), y1 - HEAD_R - 1)
    cv2.circle(img, head_c, HEAD_R, SKIN, -1)
    if helmet:
        cv2.ellipse(img, (head_c[0], head_c[1] - 1), (HEAD_R + 2, HEAD_R + 1), 0, 180, 360, HELMET, -1)


def _draw_vehicle(img: Frame, x: float, y: float) -> None:
    x1, y2 = int(x - CAR_W / 2), int(y)
    y1 = y2 - CAR_H
    cv2.rectangle(img, (x1, y1), (x1 + CAR_W, y2), VEHICLE, -1)
    cv2.rectangle(img, (x1 + 12, y1 + 6), (x1 + 34, y1 + 18), WINDOW, -1)
    cv2.rectangle(img, (x1 + 48, y1 + 6), (x1 + 70, y1 + 18), WINDOW, -1)


class SceneRenderer:
    def __init__(self, scene: Scene) -> None:
        self.scene = scene
        self._bg = _background(scene.seed)

    @property
    def frame_count(self) -> int:
        return int(self.scene.duration * FPS)

    def render(self, t: float) -> Frame:
        img = self._bg.copy()
        vehicles = [a for a in self.scene.actors if a.kind == "vehicle"]
        people = [a for a in self.scene.actors if a.kind == "person"]
        for actor in [*vehicles, *people]:
            pos = actor.position(t)
            if pos is None:
                continue
            if actor.kind == "vehicle":
                _draw_vehicle(img, *pos)
            else:
                _draw_person(img, pos[0], pos[1], actor.helmet)
        return img

    def visible(self, t: float, kind: str) -> int:
        n = 0
        for a in self.scene.actors:
            pos = a.position(t)
            if a.kind == kind and pos is not None and -CAR_W / 2 < pos[0] < WIDTH + CAR_W / 2:
                n += 1
        return n


def write_scene(scene: Scene, path: Path) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    renderer = SceneRenderer(scene)
    tmp = path.with_suffix(".tmp.mp4")
    writer = cv2.VideoWriter(str(tmp), cv2.VideoWriter.fourcc(*"mp4v"), FPS, (WIDTH, HEIGHT))
    if not writer.isOpened():
        raise RuntimeError(f"cannot open video writer for {tmp}")
    try:
        for i in range(renderer.frame_count):
            writer.write(renderer.render(i / FPS))
    finally:
        writer.release()
    tmp.replace(path)
    return path


def ensure_samples(directory: Path, names: Sequence[str] | None = None) -> list[Path]:
    """Generate any missing sample videos (idempotent)."""
    out: list[Path] = []
    for name in names or SCENES:
        target = directory / f"{name}.mp4"
        if not target.exists():
            log.info("generating sample video %s", target)
            write_scene(SCENES[name], target)
        out.append(target)
    return out


def main() -> None:
    parser = argparse.ArgumentParser(description="Generate VisionGuard synthetic sample videos")
    parser.add_argument("--out", type=Path, default=Path("data/videos/samples"))
    parser.add_argument("--force", action="store_true")
    args = parser.parse_args()
    if args.force:
        for name in SCENES:
            (args.out / f"{name}.mp4").unlink(missing_ok=True)
    for p in ensure_samples(args.out):
        print(f"{p}  {p.stat().st_size / 1024:.0f} KiB")


if __name__ == "__main__":
    main()
