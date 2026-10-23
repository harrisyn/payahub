import { readFile } from "node:fs/promises";
import { join } from "node:path";

import { ImageResponse } from "next/og";

import { site } from "./site";

export const alt = `${site.name}: every claim, to every insurer, through one hub`;
export const size = { width: 1200, height: 630 };
export const contentType = "image/png";

// The share card: the hero's line on Night, with the hub drawn as four lanes in, six out.
export default async function OpenGraphImage() {
  const mark = await readFile(join(process.cwd(), "public/brand/symbol-color-1024.png"));
  const markSrc = `data:image/png;base64,${mark.toString("base64")}`;
  const lanes = [150, 270, 390, 510];
  const outs = [110, 190, 270, 350, 430, 510];
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          background: "linear-gradient(160deg, #1D1238 0%, #140A26 60%)",
          color: "#fff",
          padding: "64px 72px",
          position: "relative",
        }}
      >
        <svg width="1200" height="630" style={{ position: "absolute", left: 0, top: 0 }}>
          {lanes.map((y) => (
            <path key={y} d={`M760 ${y} C860 ${y} 860 315 920 315`} stroke="#8C8EFF" strokeOpacity="0.5" strokeWidth="2" fill="none" />
          ))}
          {outs.map((y) => (
            <path key={y} d={`M1000 315 C1060 315 1060 ${y} 1140 ${y}`} stroke="#7BE0B0" strokeOpacity="0.5" strokeWidth="2" fill="none" />
          ))}
          <rect x="905" y="260" width="110" height="110" rx="26" fill="#F3F0FA" />
        </svg>
        <img src={markSrc} width={84} height={84} style={{ position: "absolute", left: 918, top: 273 }} alt="" />
        <div style={{ display: "flex", flexDirection: "column", justifyContent: "space-between", width: 700 }}>
          <div style={{ fontSize: 34, fontWeight: 700, letterSpacing: -1 }}>payahub</div>
          <div style={{ display: "flex", flexDirection: "column" }}>
            <div style={{ fontSize: 76, fontWeight: 700, lineHeight: 1.02, letterSpacing: -2 }}>Every claim, to every insurer.</div>
            <div style={{ marginTop: 24, fontSize: 30, color: "rgba(255,255,255,0.72)" }}>
              One API for insurance claims in Ghana.
            </div>
          </div>
        </div>
      </div>
    ),
    size,
  );
}
