import { forwardRef } from "react";
import type { HTMLAttributes, ReactNode } from "react";

import "./ToolFrame.css";

export interface ToolFrameProps extends Omit<HTMLAttributes<HTMLDivElement>, "style" | "color"> {
  header: ReactNode;
  sidebar?: ReactNode;
  inspector?: ReactNode;
}

export const ToolFrame = forwardRef<HTMLDivElement, ToolFrameProps>(function ToolFrame(
  { className, header, sidebar, inspector, children, ...props },
  ref,
) {
  return <div ref={ref} className={["cc-tool-frame", className].filter(Boolean).join(" ")} data-layout="tool-frame" {...props}><div className="cc-tool-frame__header">{header}</div><div className="cc-tool-frame__body">{sidebar ? <div className="cc-tool-frame__sidebar">{sidebar}</div> : null}<main className="cc-tool-frame__main">{children}</main>{inspector ? <div className="cc-tool-frame__inspector">{inspector}</div> : null}</div></div>;
});
