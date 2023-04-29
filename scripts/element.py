import json
import os
from typing import List

import nltk
from unstructured.file_utils.filetype import FileType, detect_filetype
from unstructured.partition.auto import partition
from unstructured.partition.doc import partition_doc
from unstructured.partition.html import partition_html
from unstructured.partition.md import partition_md

script_path = os.path.dirname(__file__)
nltk.data.path = [os.path.join(os.path.dirname(__file__), "nltk_data")] + nltk.data.path


def get_elements(file_path, **unstructured_kwargs) -> List:
    res = []
    for f in read_files(file_path):
        try:
            filetype = detect_filetype(filename=f)
            if filetype == FileType.MD:
                elements = partition_md(filename=f, include_metadata=False, **unstructured_kwargs)
            elif filetype == FileType.HTML:
                elements = partition_html(filename=f, include_metadata=False, **unstructured_kwargs)
            elif filetype == FileType.DOC:
                elements = partition_doc(filename=f, **unstructured_kwargs)
            else:
                elements = partition(filename=f, **unstructured_kwargs)
            res.extend(_element(f, elements))
        except ValueError as e:
            print(f"Error: {e}")
    return res


def _element(f, elements) -> List:
    res = []
    group = 0
    for element in elements:
        if hasattr(element, "tag") and element.tag == "h2":
            group += 1
        metadata = {
            "title": os.path.basename(f),
            "group": str(group),
        }
        if hasattr(element, "metadata"):
            metadata.update(element.metadata.to_dict())
        if hasattr(element, "category"):
            metadata["category"] = element.category
        res.append({"content": str(element), "metadata": metadata})
    return res


def read_files(ps):
    if isinstance(ps, str):
        if not os.path.exists(ps):
            print("not found")
            return []
        elif os.path.isfile(ps):
            return [ps]
        elif os.path.isdir(ps):
            fs = []
            for root, dirs, files in os.walk(ps):
                for f in files:
                    # if f.endswith(".md") or f.endswith(".rst"):
                    fs.append(os.path.join(root, f))
            return fs
    elif isinstance(ps, list):
        fs = []
        for p in ps:
            fs.append(read_files(p))
        return fs


def write_elements(elements, output_path):
    elements_json = json.dumps(elements, indent=4)

    with open(output_path, "w") as f:
        f.write(elements_json)


def main():
    dir_path = "/Users/weiwei/go/src/github.com/juicedata/juicefs/docs/zh_cn/administration/monitoring.md"
    dir_path = os.getenv("DOCS_DIR", dir_path)
    doc_elements = get_elements(dir_path)
    write_elements(doc_elements, os.path.join(script_path, "output.json"))


if __name__ == "__main__":
    main()
