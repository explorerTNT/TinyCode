from .read import read_file
from .write import write_file
from .edit import edit_file
from .grep import search_files
from .glob import list_files
from .bash import run_bash
from .ask_user import ask_user
from .web_search import web_search
from .web_fetch import web_fetch


def get_tools(config=None):
    return [
        read_file, write_file, edit_file,
        search_files, list_files, run_bash, ask_user,
        web_search, web_fetch,
    ]


__all__ = [
    "get_tools", "read_file", "write_file", "edit_file",
    "search_files", "list_files", "run_bash", "ask_user",
    "web_search", "web_fetch",
]
