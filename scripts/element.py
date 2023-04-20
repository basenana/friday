import json
import os
from typing import List

import nltk
from unstructured.partition.auto import partition

script_path = os.path.dirname(__file__)
nltk.data.path = [os.path.join(os.path.dirname(__file__), "nltk_data")] + nltk.data.path


def get_elements(file_path, **unstructured_kwargs) -> List:
    res = []
    for f in read_files(file_path):
        try:
            elements = partition(filename=f, **unstructured_kwargs)
            for element in elements:
                metadata = {
                    "title": os.path.basename(f),
                }
                if hasattr(element, "metadata"):
                    metadata.update(element.metadata.to_dict())
                if hasattr(element, "category"):
                    metadata["category"] = element.category
                res.append({"content": str(element), "metadata": metadata})
        except ValueError as e:
            print(f"Error: {e}")
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
    dir_path = "/Users/weiwei/go/src/github.com/juicedata/juicefs/docs/zh_cn"
    dir_path = os.getenv("DOCS_DIR", dir_path)
    doc_elements = get_elements(dir_path)
    write_elements(doc_elements, os.path.join(script_path, "output.json"))


if __name__ == "__main__":
    main()
