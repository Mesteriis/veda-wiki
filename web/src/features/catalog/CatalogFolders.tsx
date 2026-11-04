import type { Page } from "../../api/types";
import { I } from "../../components/Icon";
import type { TreeNode } from "../../lib/tree";

/** A folder chosen in the catalog; the label lists the source and folders. */
export interface FolderSelection {
  sourceID: string;
  prefix: string;
  label: string;
}

export interface CatalogFoldersProps {
  node: TreeNode<Page>;
  sourceID: string;
  prefix: string;
  selection: FolderSelection | null;
  onSelect: (selection: FolderSelection) => void;
  parents?: string[];
}

export function CatalogFolders({ node, sourceID, prefix, selection, onSelect, parents = [] }: CatalogFoldersProps) {
  return [...node.folders.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([name, child]) => {
    const folderPrefix = prefix + name + "/";
    const label = [...parents, name];
    return <details className="catalog-branch catalog-subfolder" key={folderPrefix}>
      <summary><I name="chevron-right" /><button className={selection?.prefix === folderPrefix ? "catalog-folder active" : "catalog-folder"} onClick={(event) => { event.preventDefault(); onSelect({ sourceID, prefix: folderPrefix, label: label.join(" / ") }); const details = event.currentTarget.closest("details"); if (details) details.open = true; }}><I name="folder" /><span>{name}</span></button></summary>
      <CatalogFolders node={child} sourceID={sourceID} prefix={folderPrefix} selection={selection} onSelect={onSelect} parents={label} />
    </details>;
  });
}
