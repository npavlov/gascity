import { forwardRef } from "react";
import type { HTMLAttributes } from "react";

import { InfoIcon } from "../icons";
import type { UIIcon } from "../icons";
import type { StatusTone } from "./Badge";
import "./StatusSignal.css";

export interface StatusSignalProps extends Omit<HTMLAttributes<HTMLSpanElement>, "style" | "color" | "children"> {
  label: string;
  tone?: StatusTone;
  icon?: UIIcon;
}

export const StatusSignal = forwardRef<HTMLSpanElement, StatusSignalProps>(function StatusSignal(
  { className, label, tone = "neutral", icon: Icon = InfoIcon, ...props },
  ref,
) {
  return <span ref={ref} className={["cc-status-signal", className].filter(Boolean).join(" ")} data-tone={tone} {...props}><Icon aria-hidden="true" focusable="false" /><span>{label}</span></span>;
});
