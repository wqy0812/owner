import { File, Upload } from 'lucide-react';
import { useState, type InputHTMLAttributes } from 'react';
import { formatFileSize } from './Primitives';
import './FilePicker.css';

type FilePickerProps = Pick<InputHTMLAttributes<HTMLInputElement>, 'accept' | 'required' | 'disabled' | 'onChange' | 'aria-label'>;

export function FilePicker({ onChange, ...props }: FilePickerProps) {
  const [file, setFile] = useState<globalThis.File>();

  return <span className="file-picker">
    <input {...props} className="file-picker__input" type="file" title={file?.name ?? '选择本地文件'} onChange={(event) => {
      setFile(event.currentTarget.files?.[0]);
      onChange?.(event);
    }} />
    <span className={`file-picker__surface${file ? ' file-picker__surface--selected' : ''}`} aria-hidden="true">
      <span className="file-picker__icon">{file ? <File size={19} /> : <Upload size={19} />}</span>
      <span className="file-picker__details">
        <span className="file-picker__name">{file?.name ?? '尚未选择文件'}</span>
        <span className="file-picker__meta">{file ? formatFileSize(file.size) : '从本地选择文件'}</span>
      </span>
      <span className="file-picker__action">{file ? '重新选择' : '选择文件'}</span>
    </span>
  </span>;
}
