import json
import os
import sys
import uuid
from typing import List

import nltk
from unstructured.file_utils.filetype import FileType, detect_filetype
from unstructured.partition.auto import partition
from unstructured.partition.doc import partition_doc
from unstructured.partition.html import partition_html
from unstructured.partition.md import partition_md
from unstructured.documents.elements import Text, ElementMetadata

script_path = os.path.dirname(__file__)
nltk.data.path = [os.getenv("NLTK_DATA", os.path.join(os.path.dirname(__file__), "nltk_data"))] + nltk.data.path


def get_elements(file_path, **unstructured_kwargs) -> List:
    res = []
    for f in read_files(file_path):
        try:
            filetype = detect_filetype(filename=f)
            if filetype == FileType.TXT:
                elements = partition_txt(f)
            elif filetype == FileType.MD:
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


def partition_txt(filename, **unstructured_kwargs) -> List:
    elements = []
    with open(filename, "r", encoding='utf-8') as f:
        lines = f.readlines()

    for line in lines:
        t = Text(line)
        t.metadata = ElementMetadata()
        t.metadata.filename = filename
        elements.append(t)
    return elements


def _element(f, elements) -> List:
    res = []
    group = 0
    for element in elements:
        if hasattr(element, "tag") and element.tag == "h2":
            group += 1
        metadata = {
            "source": f,
            "title": os.path.basename(f),
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
    elements_json = json.dumps(elements, indent=4, ensure_ascii=False)

    with open(output_path, "w", encoding='utf-8') as f:
        f.write(elements_json)


def main(dir_path, output_path):
    doc_elements = get_elements(dir_path)
    write_elements(doc_elements, output_path)


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Please provide a directory path you want to parse.")
        sys.exit(1)
    if len(sys.argv) < 3:
        output = os.path.join(script_path, "output.json")
    else:
        output = sys.argv[2]
    main(sys.argv[1], output)
