import { Form, type FormItemProps } from 'antd';
import { cloneElement, isValidElement, useId, type ReactElement } from 'react';

/** Shared field spacing and a real label association, including controlled fields outside forms. */
export function Field({ children, ...props }: FormItemProps) {
  const id = useId();
  return <Form.Item layout="vertical" {...props} htmlFor={id}>
    {isValidElement(children) ? cloneElement(children as ReactElement<{ id?: string }>, { id }) : children}
  </Form.Item>;
}
