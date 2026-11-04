import type { ReactNode } from "react";

export interface PanelProps {
  title: ReactNode;
  children?: ReactNode;
  className?: string;
  actions?: ReactNode;
}

export function Panel({ title, children, className = "", actions }: PanelProps) {
  return (
    <section className={"panel " + className}>
      <div className="section-heading">
        <h2>{title}</h2>
        {actions}
      </div>
      {children}
    </section>
  );
}
