import { forwardRef, useId } from "react";
import type { HTMLAttributes, ReactNode } from "react";

import type { UIIcon } from "../icons";
import "./EmptyState.css";

export interface EmptyStateProps extends Omit<HTMLAttributes<HTMLElement>, "style" | "color" | "title"> {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  icon?: UIIcon;
}

export const EmptyState = forwardRef<HTMLElement, EmptyStateProps>(function EmptyState(
  { className, title, description, actions, icon: Icon, ...props },
  ref,
) {
  const titleID = useId();
  return <section ref={ref} className={["cc-empty-state", className].filter(Boolean).join(" ")} aria-labelledby={titleID} {...props}>{Icon ? <Icon aria-hidden="true" focusable="false" /> : null}<h3 id={titleID}>{title}</h3>{description ? <div className="cc-empty-state__description">{description}</div> : null}{actions ? <div className="cc-empty-state__actions">{actions}</div> : null}</section>;
});
