import { Button, Upload } from 'antd';
import { File, Upload as UploadIcon } from 'lucide-react';
import { useState } from 'react';
import { formatFileSize } from './Primitives';
import './FilePicker.css';

interface FilePickerProps {
  id?: string;
  accept?: string;
  required?: boolean;
  disabled?: boolean;
  'aria-label'?: string;
  onChange?: (file: globalThis.File) => void;
}
export function FilePicker({ onChange, id, required, ...props }: FilePickerProps) {
  const [file, setFile] = useState<globalThis.File>();
  return <div className="file-picker">
    <Upload accept={props.accept} disabled={props.disabled} maxCount={1} showUploadList={false}
      beforeUpload={selected => { setFile(selected); onChange?.(selected); return false; }}>
      <Button id={id} disabled={props.disabled} aria-label={props['aria-label']} aria-required={required} icon={<UploadIcon size={16} />}>{file ? '重新选择' : '选择文件'}</Button>
    </Upload>
    <span className="file-picker__summary">{file ? <><File size={16} /><span>{file.name}<small>{formatFileSize(file.size)}</small></span></> : '尚未选择文件'}</span>
  </div>;
}
