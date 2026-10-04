import type { ComponentProps } from "react";
import { Badge } from "../../components/ui/Badge";

export function QuotaBadge({
  children,
  ...props
}: ComponentProps<typeof Badge>) {
  return (
    <Badge {...props}>
      <span className="font-sans">{children}</span>
    </Badge>
  );
}
