import type { ImgHTMLAttributes } from "react";

export type IconProps = { name: string } & ImgHTMLAttributes<HTMLImageElement>;

// Decorative icon from the bundled set; labels belong to the enclosing control.
export function I({ name, ...rest }: IconProps) {
  return (
    <img
      className="icon"
      src={"/assets/icons/" + name + ".svg"}
      alt=""
      aria-hidden="true"
      {...rest}
    />
  );
}
