import { Button, type ButtonProps } from 'antd';
import { useNavigate } from 'react-router-dom';

/** An Ant button with normal link semantics and client-side routing. */
export function RouteButton({ to, onClick, ...props }: ButtonProps & { to: string }) {
  const navigate = useNavigate();
  return <Button {...props} href={to} onClick={event => {
    onClick?.(event);
    if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
    event.preventDefault();
    navigate(to);
  }} />;
}
