import { forwardRef, useId } from "react";
import type { HTMLAttributes, ReactNode } from "react";

import "./DetailHeader.css";

export interface DetailHeaderProps extends Omit<HTMLAttributes<HTMLElement>, "style" | "color" | "title"> {
  title: ReactNode;
  subtitle?: ReactNode;
  status?: ReactNode;
  actions?: ReactNode;
}

export const DetailHeader = forwardRef<HTMLElement, DetailHeaderProps>(function DetailHeader(
  { className, title, subtitle, status, actions, ...props },
  ref,
) {
  const titleID = useId();
  return <header ref={ref} className={["cc-detail-header", className].filter(Boolean).join(" ")} aria-labelledby={titleID} {...props}><div className="cc-detail-header__identity"><h1 id={titleID}>{title}</h1>{subtitle ? <div className="cc-detail-header__subtitle">{subtitle}</div> : null}</div>{status ? <div className="cc-detail-header__status">{status}</div> : null}{actions ? <div className="cc-detail-header__actions">{actions}</div> : null}</header>;
});
