import { useEffect, useRef, useState } from "react";

type LoginBeamsBackgroundProps = {
  mode: "dark" | "light";
};

type Beam = {
  x: number;
  y: number;
  width: number;
  length: number;
  angle: number;
  speed: number;
  opacity: number;
  hue: number;
  pulse: number;
  pulseSpeed: number;
};

const DESKTOP_QUERY = "(min-width: 901px)";
const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";
const FRAME_INTERVAL = 1000 / 30;
const MAX_DELTA_SECONDS = 0.08;
const DPR_CAP = 1.5;

function seededRandom(seed: number) {
  let state = seed;
  return () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let value = Math.imul(state ^ (state >>> 15), 1 | state);
    value = (value + Math.imul(value ^ (value >>> 7), 61 | value)) ^ value;
    return ((value ^ (value >>> 14)) >>> 0) / 4294967296;
  };
}

function createBeams(
  width: number,
  height: number,
  mode: LoginBeamsBackgroundProps["mode"],
) {
  const random = seededRandom(mode === "dark" ? 0x0a17fa11 : 0x11fa170a);
  const count = Math.max(8, Math.min(12, Math.round(width / 150)));
  const hueStart = mode === "dark" ? 188 : 194;
  const hueRange = mode === "dark" ? 30 : 22;
  const opacityStart = mode === "dark" ? 0.045 : 0.025;
  const opacityRange = mode === "dark" ? 0.055 : 0.035;

  return Array.from({ length: count }, (_, index): Beam => {
    const columnCenter = ((index + 0.5) / count) * width;
    return {
      x: columnCenter + (random() - 0.5) * (width / count) * 1.4,
      y: random() * height * 2 - height * 0.45,
      width: 72 + random() * 96,
      length: height * (1.35 + random() * 0.55),
      angle: -29 + random() * 8,
      speed: 5 + random() * 7,
      opacity: opacityStart + random() * opacityRange,
      hue: hueStart + random() * hueRange,
      pulse: random() * Math.PI * 2,
      pulseSpeed: 0.16 + random() * 0.16,
    };
  });
}

function resetBeam(beam: Beam, width: number, height: number, index: number) {
  const columns = 4;
  const column = index % columns;
  const spacing = width / columns;
  beam.x = column * spacing + spacing * (0.35 + (index % 3) * 0.14);
  beam.y = height * 1.2;
}

/**
 * Login-page adaptation of Kokonut UI's MIT-licensed Beams Background.
 *
 * Upstream source:
 * https://github.com/kokonut-labs/kokonutui/blob/main/components/kokonutui/beams-background.tsx
 *
 * The Canvas 2D beam construction is retained. This host narrows the palette,
 * caps rendering at 30fps, sizes to its container, pauses when hidden, and
 * removes the upstream demo content and Motion-only overlay.
 */
export function LoginBeamsBackground({ mode }: LoginBeamsBackgroundProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const [enabled, setEnabled] = useState(false);

  useEffect(() => {
    const desktop = window.matchMedia(DESKTOP_QUERY);
    const reducedMotion = window.matchMedia(REDUCED_MOTION_QUERY);
    const update = () => setEnabled(desktop.matches && !reducedMotion.matches);

    desktop.addEventListener("change", update);
    reducedMotion.addEventListener("change", update);
    update();

    return () => {
      desktop.removeEventListener("change", update);
      reducedMotion.removeEventListener("change", update);
    };
  }, []);

  useEffect(() => {
    if (!enabled) return;

    const root = rootRef.current;
    const canvas = canvasRef.current;
    const context = canvas?.getContext("2d");
    if (!root || !canvas || !context) return;

    let animationFrameId = 0;
    let lastFrameTime = 0;
    let lastPaintTime = 0;
    let width = 0;
    let height = 0;
    let dpr = 1;
    let beams: Beam[] = [];
    let inView = true;

    const canAnimate = () => inView && document.visibilityState === "visible";

    const clearCanvas = () => {
      context.setTransform(1, 0, 0, 1, 0, 0);
      context.clearRect(0, 0, canvas.width, canvas.height);
      context.setTransform(dpr, 0, 0, dpr, 0, 0);
    };

    const drawBeam = (beam: Beam) => {
      const saturation = mode === "dark" ? 38 : 30;
      const lightness = mode === "dark" ? 64 : 43;
      const pulse = 0.84 + Math.sin(beam.pulse) * 0.16;
      const opacity = beam.opacity * pulse;
      const gradient = context.createLinearGradient(0, 0, 0, beam.length);

      gradient.addColorStop(
        0,
        `hsla(${beam.hue}, ${saturation}%, ${lightness}%, 0)`,
      );
      gradient.addColorStop(
        0.22,
        `hsla(${beam.hue}, ${saturation}%, ${lightness}%, ${opacity * 0.42})`,
      );
      gradient.addColorStop(
        0.5,
        `hsla(${beam.hue}, ${saturation}%, ${lightness}%, ${opacity})`,
      );
      gradient.addColorStop(
        0.78,
        `hsla(${beam.hue}, ${saturation}%, ${lightness}%, ${opacity * 0.38})`,
      );
      gradient.addColorStop(
        1,
        `hsla(${beam.hue}, ${saturation}%, ${lightness}%, 0)`,
      );

      context.save();
      context.translate(beam.x, beam.y);
      context.rotate((beam.angle * Math.PI) / 180);
      context.fillStyle = gradient;
      context.fillRect(-beam.width / 2, 0, beam.width, beam.length);
      context.restore();
    };

    const paint = (deltaSeconds: number) => {
      clearCanvas();
      context.filter = "blur(34px)";
      context.globalCompositeOperation =
        mode === "dark" ? "screen" : "multiply";

      beams.forEach((beam, index) => {
        beam.y -= beam.speed * deltaSeconds;
        beam.pulse += beam.pulseSpeed * deltaSeconds;
        if (beam.y + beam.length < -height * 0.2) {
          resetBeam(beam, width, height, index);
        }
        drawBeam(beam);
      });

      context.filter = "none";
      context.globalCompositeOperation = "source-over";
      canvas.dataset.ready = "true";
    };

    const animate = (time: number) => {
      animationFrameId = 0;
      if (!canAnimate()) return;

      if (time - lastPaintTime >= FRAME_INTERVAL) {
        const deltaSeconds = lastFrameTime
          ? Math.min((time - lastFrameTime) / 1000, MAX_DELTA_SECONDS)
          : 0;
        paint(deltaSeconds);
        lastFrameTime = time;
        lastPaintTime = time;
      }

      animationFrameId = requestAnimationFrame(animate);
    };

    const start = () => {
      if (!animationFrameId && canAnimate()) {
        lastFrameTime = 0;
        animationFrameId = requestAnimationFrame(animate);
      }
    };

    const stop = () => {
      if (animationFrameId) cancelAnimationFrame(animationFrameId);
      animationFrameId = 0;
    };

    const resize = () => {
      const rect = root.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0) return;

      width = rect.width;
      height = rect.height;
      dpr = Math.min(window.devicePixelRatio || 1, DPR_CAP);
      canvas.width = Math.round(width * dpr);
      canvas.height = Math.round(height * dpr);
      canvas.style.width = `${width}px`;
      canvas.style.height = `${height}px`;
      beams = createBeams(width, height, mode);
      paint(0);
      start();
    };

    const handleVisibilityChange = () => {
      if (canAnimate()) start();
      else stop();
    };

    const resizeObserver = new ResizeObserver(resize);
    resizeObserver.observe(root);

    const intersectionObserver = new IntersectionObserver(([entry]) => {
      inView = entry.isIntersecting;
      handleVisibilityChange();
    });
    intersectionObserver.observe(root);
    document.addEventListener("visibilitychange", handleVisibilityChange);
    resize();

    return () => {
      stop();
      resizeObserver.disconnect();
      intersectionObserver.disconnect();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [enabled, mode]);

  return (
    <div
      ref={rootRef}
      className="ag-login-beams"
      aria-hidden="true"
      data-active={enabled ? "true" : "false"}
      data-color-mode={mode}
      data-kokonutui-component="beams-background"
    >
      {enabled ? <canvas ref={canvasRef} /> : null}
    </div>
  );
}
