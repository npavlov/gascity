import { forwardRef, useId } from "react";
import type { HTMLAttributes, ReactNode } from "react";

import "./Panel.css";

export interface PanelProps extends Omit<HTMLAttributes<HTMLElement>, "style" | "color" | "title"> {
  title: ReactNode;
  actions?: ReactNode;
}

export const Panel = forwardRef<HTMLElement, PanelProps>(function Panel(
  { className, title, actions, children, ...props },
  ref,
) {
  const titleID = useId();
  return <section ref={ref} className={["cc-panel", className].filter(Boolean).join(" ")} aria-labelledby={titleID} {...props}><header className="cc-panel__header"><h2 id={titleID}>{title}</h2>{actions ? <div className="cc-panel__actions">{actions}</div> : null}</header><div className="cc-panel__body">{children}</div></section>;
});
