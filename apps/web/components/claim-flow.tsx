"use client";

import { useEffect, useRef, useState } from "react";

// ClaimFlow is the hero's picture of what Payahub does: claims leave a
// clinic in whatever form it bills in, pass through the hub where they are
// checked and coded, and reach each insurer, which acknowledges them.
// The diagram is server-rendered and readable without script; the moving
// claims are drawn on top once it is on screen and motion is allowed.

type Pt = { x: number; y: number };
type Geometry = {
  w: number;
  h: number;
  vertical: boolean;
  sources: Pt[];
  hub: Pt & { w: number; h: number };
  payers: Pt[];
  node: { w: number; h: number };
};

// Only the API is live today; the other routes are on the build plan and say so.
export const sources = [
  { label: "Hospital system", sub: "REST API" },
  { label: "Spreadsheet", sub: "CSV or Excel · soon" },
  { label: "Scanned forms", sub: "PDF or photo · soon" },
  { label: "Web form", sub: "In the console · soon" },
];

// The platforms whose connectors exist today, plus insurers reached directly.
export const payers = [
  { label: "XCeli", sub: "Platform" },
  { label: "Medserve", sub: "Platform" },
  { label: "SmartClaims", sub: "Platform" },
  { label: "RX Insurance", sub: "Platform" },
  { label: "ClaimIQ", sub: "Platform" },
  { label: "Direct insurers", sub: "Their own API" },
];

const wide: Geometry = {
  w: 1200,
  h: 540,
  vertical: false,
  sources: [95, 215, 335, 455].map((y) => ({ x: 130, y })),
  hub: { x: 600, y: 275, w: 230, h: 230 },
  payers: [60, 145, 230, 315, 400, 485].map((y) => ({ x: 1070, y })),
  node: { w: 210, h: 66 },
};

const tall: Geometry = {
  w: 360,
  h: 660,
  vertical: true,
  sources: [
    { x: 92, y: 58 },
    { x: 268, y: 58 },
    { x: 92, y: 132 },
    { x: 268, y: 132 },
  ],
  hub: { x: 180, y: 330, w: 170, h: 150 },
  payers: [
    { x: 92, y: 516 },
    { x: 268, y: 516 },
    { x: 92, y: 568 },
    { x: 268, y: 568 },
    { x: 92, y: 620 },
    { x: 268, y: 620 },
  ],
  node: { w: 164, h: 46 },
};

// Wires leave a node's edge and enter the hub spread along its side, so they
// read as separate lanes rather than one bundle.
function inbound(g: Geometry, i: number) {
  const s = g.sources[i];
  const n = g.sources.length;
  if (!g.vertical) {
    const x0 = s.x + g.node.w / 2;
    const x1 = g.hub.x - g.hub.w / 2;
    const y1 = g.hub.y + (i - (n - 1) / 2) * 30;
    const mx = (x0 + x1) / 2;
    return `M${x0} ${s.y} C${mx} ${s.y} ${mx} ${y1} ${x1} ${y1}`;
  }
  const y0 = s.y + g.node.h / 2;
  const y1 = g.hub.y - g.hub.h / 2;
  const x1 = g.hub.x + (i - (n - 1) / 2) * 26;
  const my = (y0 + y1) / 2;
  return `M${s.x} ${y0} C${s.x} ${my} ${x1} ${my} ${x1} ${y1}`;
}

function outbound(g: Geometry, j: number) {
  const p = g.payers[j];
  const n = g.payers.length;
  if (!g.vertical) {
    const x0 = g.hub.x + g.hub.w / 2;
    const y0 = g.hub.y + (j - (n - 1) / 2) * 24;
    const x1 = p.x - g.node.w / 2;
    const mx = (x0 + x1) / 2;
    return `M${x0} ${y0} C${mx} ${y0} ${mx} ${p.y} ${x1} ${p.y}`;
  }
  const x0 = g.hub.x + (j - (n - 1) / 2) * 22;
  const y0 = g.hub.y + g.hub.h / 2;
  // The lower rows' wires stop above the first row instead of crossing it.
  const y1 = g.payers[0].y - g.node.h / 2;
  const my = (y0 + y1) / 2;
  return `M${x0} ${y0} C${x0} ${my} ${p.x} ${my} ${p.x} ${y1}`;
}

// Claims the hero sends, each a real shape of claim: who, what, how much.
const claims = [
  { clinic: "Ridge Clinic", what: "GP consultation", amount: "GHS 150.00" },
  { clinic: "Asylum Down Pharmacy", what: "Artemether-lumefantrine", amount: "GHS 64.50" },
  { clinic: "Tema Family Hospital", what: "Full blood count", amount: "GHS 85.00" },
  { clinic: "Kasoa Medical Centre", what: "Malaria RDT", amount: "GHS 40.00" },
  { clinic: "Osu Eye Clinic", what: "Refraction test", amount: "GHS 120.00" },
  { clinic: "Ho Polyclinic", what: "Antenatal visit", amount: "GHS 95.00" },
];

type Packet = {
  el: SVGCircleElement;
  trail: SVGCircleElement;
  src: number;
  dst: number;
  claim: number;
  t0: number;
  stage: "in" | "hub" | "out";
};

const IN_MS = 1500;
const HUB_MS = 520;
const OUT_MS = 1500;
const EVERY_MS = 760;

type Arrival = { id: number; claim: number; payer: number };

function Diagram({ g, live }: { g: Geometry; live: boolean }) {
  const root = useRef<HTMLDivElement>(null);
  const layer = useRef<SVGGElement>(null);
  const inPaths = useRef<(SVGPathElement | null)[]>([]);
  const outPaths = useRef<(SVGPathElement | null)[]>([]);
  const [arrivals, setArrivals] = useState<Arrival[]>([]);
  const [checking, setChecking] = useState<number | null>(null);

  useEffect(() => {
    if (!live) return;
    const host = root.current;
    const svgLayer = layer.current;
    if (!host || !svgLayer) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;

    let raf = 0;
    let visible = false;
    let last = 0;
    let seq = 0;
    const packets: Packet[] = [];
    const ns = "http://www.w3.org/2000/svg";

    const spawn = (now: number) => {
      const src = seq % g.sources.length;
      const dst = (seq * 5 + 1) % g.payers.length;
      const claim = seq % claims.length;
      seq++;
      const trail = document.createElementNS(ns, "circle");
      trail.setAttribute("r", g.vertical ? "7" : "9");
      trail.setAttribute("class", "flow__glow");
      const el = document.createElementNS(ns, "circle");
      el.setAttribute("r", g.vertical ? "3.5" : "4.5");
      el.setAttribute("class", "flow__dot");
      svgLayer.append(trail, el);
      packets.push({ el, trail, src, dst, claim, t0: now, stage: "in" });
    };

    const place = (p: Packet, path: SVGPathElement, f: number) => {
      const len = path.getTotalLength();
      // Ease in and out so a claim leaves gently and settles on arrival.
      const e = f < 0.5 ? 2 * f * f : 1 - Math.pow(-2 * f + 2, 2) / 2;
      const pt = path.getPointAtLength(e * len);
      p.el.setAttribute("cx", String(pt.x));
      p.el.setAttribute("cy", String(pt.y));
      p.trail.setAttribute("cx", String(pt.x));
      p.trail.setAttribute("cy", String(pt.y));
    };

    const frame = (now: number) => {
      raf = requestAnimationFrame(frame);
      if (!visible) return;
      if (now - last > EVERY_MS) {
        last = now;
        spawn(now);
      }
      for (let i = packets.length - 1; i >= 0; i--) {
        const p = packets[i];
        const age = now - p.t0;
        if (p.stage === "in") {
          const path = inPaths.current[p.src];
          if (!path) continue;
          if (age >= IN_MS) {
            p.stage = "hub";
            p.el.setAttribute("opacity", "0");
            p.trail.setAttribute("opacity", "0");
            setChecking(p.claim);
            host.dataset.pulse = String((Number(host.dataset.pulse ?? 0) + 1) % 2);
          } else place(p, path, age / IN_MS);
        } else if (p.stage === "hub") {
          if (age >= IN_MS + HUB_MS) {
            p.stage = "out";
            p.el.removeAttribute("opacity");
            p.trail.removeAttribute("opacity");
            p.el.setAttribute("class", "flow__dot flow__dot--out");
          }
        } else {
          const path = outPaths.current[p.dst];
          const f = (age - IN_MS - HUB_MS) / OUT_MS;
          if (!path) continue;
          if (f >= 1) {
            p.el.remove();
            p.trail.remove();
            packets.splice(i, 1);
            const node = host.querySelector<HTMLElement>(`[data-payer="${p.dst}"]`);
            if (node) {
              node.classList.remove("is-ack");
              void node.offsetWidth; // restart the flash
              node.classList.add("is-ack");
            }
            setArrivals((a) => [{ id: now, claim: p.claim, payer: p.dst }, ...a].slice(0, 3));
          } else place(p, path, f);
        }
      }
    };

    const io = new IntersectionObserver(([e]) => {
      visible = e.isIntersecting && document.visibilityState === "visible";
      if (!visible) {
        // Drop claims in flight rather than letting them jump on return.
        for (const p of packets) {
          p.el.remove();
          p.trail.remove();
        }
        packets.length = 0;
      }
    });
    io.observe(host);
    const onVis = () => {
      if (document.visibilityState !== "visible") visible = false;
      else visible = host.getBoundingClientRect().bottom > 0 && host.getBoundingClientRect().top < innerHeight;
    };
    document.addEventListener("visibilitychange", onVis);
    raf = requestAnimationFrame(frame);
    return () => {
      cancelAnimationFrame(raf);
      io.disconnect();
      document.removeEventListener("visibilitychange", onVis);
      for (const p of packets) {
        p.el.remove();
        p.trail.remove();
      }
    };
  }, [g, live]);

  const pct = (v: number, of: number) => `${(v / of) * 100}%`;
  const shown = checking ?? 0;

  return (
    <div
      ref={root}
      className={`flow ${g.vertical ? "flow--tall" : "flow--wide"}`}
      style={{ aspectRatio: `${g.w} / ${g.h}` }}
    >
      <svg className="flow__wires" viewBox={`0 0 ${g.w} ${g.h}`} aria-hidden="true">
        <defs>
          <linearGradient id={`wire-in-${g.vertical ? "t" : "w"}`} x1="0" x2={g.vertical ? "0" : "1"} y1="0" y2={g.vertical ? "1" : "0"}>
            <stop offset="0" stopColor="#8C8EFF" stopOpacity="0.15" />
            <stop offset="1" stopColor="#8C8EFF" stopOpacity="0.7" />
          </linearGradient>
          <linearGradient id={`wire-out-${g.vertical ? "t" : "w"}`} x1="0" x2={g.vertical ? "0" : "1"} y1="0" y2={g.vertical ? "1" : "0"}>
            <stop offset="0" stopColor="#8C8EFF" stopOpacity="0.7" />
            <stop offset="1" stopColor="#7BE0B0" stopOpacity="0.35" />
          </linearGradient>
        </defs>
        {g.sources.map((_, i) => (
          <path
            key={`i${i}`}
            ref={(el) => {
              inPaths.current[i] = el;
            }}
            d={inbound(g, i)}
            className="flow__wire"
            stroke={`url(#wire-in-${g.vertical ? "t" : "w"})`}
          />
        ))}
        {g.payers.map((_, j) => (
          <path
            key={`o${j}`}
            ref={(el) => {
              outPaths.current[j] = el;
            }}
            d={outbound(g, j)}
            className="flow__wire"
            stroke={`url(#wire-out-${g.vertical ? "t" : "w"})`}
          />
        ))}
        <g ref={layer} />
      </svg>

      <ul className="flow__nodes" aria-label="Where claims come from">
        {sources.map((s, i) => (
          <li
            key={s.label}
            className="flow__node flow__node--src"
            style={{ left: pct(g.sources[i].x, g.w), top: pct(g.sources[i].y, g.h), width: pct(g.node.w, g.w) }}
          >
            <span className="flow__label">{s.label}</span>
            <span className="flow__sub">{s.sub}</span>
          </li>
        ))}
      </ul>

      <div
        className="flow__hub"
        style={{
          left: pct(g.hub.x, g.w),
          top: pct(g.hub.y, g.h),
          width: pct(g.hub.w, g.w),
          height: pct(g.hub.h, g.h),
        }}
      >
        <img src="/brand/payahub-symbol-small-color.svg" alt="" width={40} height={40} className="flow__hub-mark" />
        <span className="flow__hub-name">Payahub</span>
        <span className="flow__hub-steps" aria-hidden="true">
          <span>Check</span>
          <span>Code</span>
          <span>Send</span>
        </span>
        <span className="flow__hub-claim" aria-hidden="true" key={shown}>
          {claims[shown].what}
        </span>
      </div>

      <ul className="flow__nodes" aria-label="Where claims go">
        {payers.map((p, j) => (
          <li
            key={p.label}
            data-payer={j}
            className="flow__node flow__node--dst"
            style={{ left: pct(g.payers[j].x, g.w), top: pct(g.payers[j].y, g.h), width: pct(g.node.w, g.w) }}
          >
            <span className="flow__label">{p.label}</span>
            <span className="flow__sub">{p.sub}</span>
            <span className="flow__ack" aria-hidden="true">
              Acknowledged
            </span>
          </li>
        ))}
      </ul>

      {!g.vertical && (
        <ol className="flow__feed" aria-hidden="true" data-title="Sample claims">
          {(arrivals.length ? arrivals : [{ id: 0, claim: 0, payer: 0 }]).map((a) => (
            <li key={a.id} className="flow__feed-row">
              <span className="flow__feed-tick">✓</span>
              <span className="flow__feed-text">
                <strong>{claims[a.claim].clinic}</strong> · {claims[a.claim].amount} to {payers[a.payer].label}
              </span>
              <span className="flow__feed-state">Acknowledged</span>
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}

export function ClaimFlow() {
  const [live, setLive] = useState(false);
  useEffect(() => setLive(true), []);
  return (
    <figure className="flow-figure">
      <Diagram g={wide} live={live} />
      <Diagram g={tall} live={live} />
      <figcaption className="sr-only">
        Claims leave a clinic from its hospital system, a spreadsheet, scanned forms or a web form. Payahub checks and
        codes each one, then sends it to the insurer, directly or through the platform it uses, and records the
        acknowledgement.
      </figcaption>
    </figure>
  );
}
