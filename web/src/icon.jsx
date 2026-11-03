// Decorative icon from the bundled set; labels belong to the enclosing control.
export function I({ name, ...rest }) {
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
