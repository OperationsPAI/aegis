import textwrap
from io import StringIO
from pathlib import Path
from typing import Literal

import pandas as pd
import polars as pl

from ..logging import logger


def assert_columns(df: pl.DataFrame, cols: list[str]) -> None:
    for col in cols:
        assert col in df.columns, f"Column {col} not found in DataFrame"


def print_dataframe(df: pl.DataFrame) -> None:
    with pl.Config(
        tbl_rows=len(df),
        tbl_cols=len(df.columns),
        fmt_str_lengths=120,
        tbl_width_chars=160,
        tbl_cell_numeric_alignment="RIGHT",
    ):
        print(df)


def format_dataframe(
    df: pl.DataFrame | pd.DataFrame,
    output_format: Literal["display", "latex", "csv", "formatted_text", "html", "png"] = "display",
    output_file: str | Path | None = None,
    merge_columns: list[str] | None = None,
    wrap_text: bool = False,
    wrap_width: int = 30,
    max_rows: int | None = None,
    max_cols: int | None = None,
    **kwargs,
) -> str | None:
    """
    Unified DataFrame formatting and output function

    Args:
        df: Input DataFrame (supports both polars and pandas)
        output_format: Output format
            - 'display': Display directly in console with merge cells and smart wrapping support
            - 'latex': Export as LaTeX table format
            - 'csv': Export as CSV format
            - 'formatted_text': Export as formatted text (no width limit)
            - 'html': Export as HTML table format
            - 'png': Export as PNG image format
        output_file: Output file path, if None returns string or displays directly
        merge_columns: List of column names to merge cells (consecutive same values will be cleared)
        wrap_text: Whether to enable smart text wrapping
        wrap_width: Text wrapping width
        max_rows: Maximum number of rows to display (only for display and formatted_text)
        max_cols: Maximum number of columns to display (only for display and formatted_text)
        **kwargs: Additional parameters passed to respective export functions

    Returns:
        None for display format, formatted string for other formats

    Examples:
        # Display directly with merged cells and smart wrapping
        format_dataframe(df, merge_columns=['category'], wrap_text=True)

        # Export as LaTeX file
        format_dataframe(df, 'latex', 'output.tex', caption='My Table')

        # Export as CSV string
        csv_str = format_dataframe(df, 'csv')

        # Export as HTML file with merged columns
        format_dataframe(df, 'html', 'table.html', merge_columns=['group'])

        # Export as PNG image file with intelligent row width adjustment
        format_dataframe(df, 'png', 'table.png', dpi=300, max_cell_width=25)
    """
    # Convert to pandas DataFrame for unified processing
    if isinstance(df, pl.DataFrame):
        pdf = df.to_pandas()
    else:
        pdf = df.copy()

    # Format numeric columns to 2 decimal places
    for col in pdf.select_dtypes(include=["float64", "float32", "int64", "int32"]).columns:
        if pdf[col].dtype.kind in "fi":  # float or int
            # Round float columns to 2 decimal places
            if pdf[col].dtype.kind == "f":
                pdf[col] = pdf[col].round(2)
            # Convert int columns to float with 2 decimal places for display
            else:
                pdf[col] = pdf[col].astype(float).round(2)

    # Apply smart text wrapping
    if wrap_text:
        for col in pdf.select_dtypes(include=["object"]).columns:
            pdf[col] = pdf[col].astype(str).apply(lambda x: _smart_wrap_text(x, wrap_width))

    # Process based on format
    if output_format == "display":
        # For display, use the old approach of clearing duplicate values
        if merge_columns:
            pdf = _merge_consecutive_cells(pdf, merge_columns)

        # Display directly in console
        with pd.option_context(
            "display.max_columns",
            max_cols,
            "display.max_rows",
            max_rows,
            "display.width",
            None,
            "display.max_colwidth",
            None,
        ):
            print(pdf)
        return None

    elif output_format == "latex":
        result = _export_latex(pdf, merge_columns=merge_columns, **kwargs)
    elif output_format == "csv":
        # For CSV, merge cells by clearing duplicates
        if merge_columns:
            pdf = _merge_consecutive_cells(pdf, merge_columns)
        result = _export_csv(pdf, **kwargs)
    elif output_format == "html":
        result = _export_html(pdf, merge_columns=merge_columns, **kwargs)
    elif output_format == "png":
        # For PNG, we need to handle file saving differently
        if not output_file:
            raise ValueError("output_file must be specified for PNG format")
        _export_png(pdf, output_file, merge_columns=merge_columns, **kwargs)
        return None  # PNG format doesn't return string content
    else:  # formatted_text
        # For formatted text, merge cells by clearing duplicates
        if merge_columns:
            pdf = _merge_consecutive_cells(pdf, merge_columns)
        result = _export_formatted_text(pdf, max_rows=max_rows, max_cols=max_cols, **kwargs)

    # Save to file or return string
    if output_file and output_format != "png":  # PNG format handles file saving internally
        output_path = Path(output_file)
        output_path.parent.mkdir(parents=True, exist_ok=True)
        with open(output_path, "w", encoding="utf-8") as f:
            f.write(result)
        logger.info(f"Output saved to: {output_path}")

    return result


def _smart_wrap_text(text: str, width: int = 30) -> str:
    """Smart text wrapping, prioritize breaking at spaces to avoid word truncation"""
    if len(text) <= width:
        return text

    # Use textwrap module for smart wrapping
    lines = textwrap.wrap(text, width=width, break_long_words=False, break_on_hyphens=False)
    return "\n".join(lines)


def _escape_latex_chars(text: str) -> str:
    """Escape LaTeX special characters in text"""
    if not isinstance(text, str):
        text = str(text)

    # Escape LaTeX special characters in proper order
    for char, escape in [
        ("\\", "\\textbackslash{}"),  # Backslash must be first
        ("&", "\\&"),
        ("%", "\\%"),
        ("$", "\\$"),
        ("#", "\\#"),
        ("_", "\\_"),
        ("{", "\\{"),
        ("}", "\\}"),
        ("^", "\\textasciicircum{}"),
        ("~", "\\textasciitilde{}"),
    ]:
        text = text.replace(char, escape)
    return text


def _merge_consecutive_cells(df: pd.DataFrame, columns: list[str]) -> pd.DataFrame:
    """
    Merge consecutive cells with same values in specified columns (by clearing duplicate values)

    Args:
        df: Input pandas DataFrame
        columns: List of column names to merge cells

    Returns:
        Processed pandas DataFrame with duplicate values replaced by empty strings
    """
    pdf = df.copy()

    for col in columns:
        if col not in pdf.columns:
            continue

        # Create a boolean mask to identify values same as previous row
        mask = pdf[col] == pdf[col].shift(1)
        # Replace same values with empty strings
        pdf.loc[mask, col] = ""

    return pdf


def _export_latex(df: pd.DataFrame, merge_columns: list[str] | None = None, **kwargs) -> str:
    """Export as LaTeX table format with real merged cells"""
    default_kwargs = {
        "index": False,
        "escape": False,
        "column_format": "l" * len(df.columns),
        "position": "h!",
        "caption": "DataFrame Table",
        "label": "tab:dataframe",
    }
    default_kwargs.update(kwargs)

    if not merge_columns:
        return df.to_latex(**default_kwargs)

    # Build LaTeX manually with merged cells using multirow
    latex_parts = []

    # Table opening
    position = default_kwargs.get("position", "h!")
    caption = default_kwargs.get("caption", "DataFrame Table")
    label = default_kwargs.get("label", "tab:dataframe")
    column_format = default_kwargs.get("column_format", "l" * len(df.columns))

    latex_parts.append(f"\\begin{{table}}[{position}]")
    latex_parts.append(f"\\caption{{{caption}}}")
    latex_parts.append(f"\\label{{{label}}}")
    latex_parts.append(f"\\begin{{tabular}}{{{column_format}}}")
    latex_parts.append("\\toprule")

    # Header with proper LaTeX escaping
    header_cols = []
    for col in df.columns:
        header_cols.append(_escape_latex_chars(str(col)))
    latex_parts.append(" & ".join(header_cols) + " \\\\")
    latex_parts.append("\\midrule")

    # Calculate rowspans for merge columns
    merge_info = _calculate_merge_spans(df, merge_columns)

    # Body with merged cells
    for i, row in df.iterrows():
        row_parts = []

        for col in df.columns:
            cell_value = row[col]
            if cell_value is None:
                cell_value = ""
            else:
                # Format numeric values to 2 decimal places
                if isinstance(cell_value, (int, float)) and not pd.isna(cell_value):
                    if isinstance(cell_value, float):
                        cell_value = f"{cell_value:.2f}"
                    else:
                        cell_value = f"{float(cell_value):.2f}"
                else:
                    cell_value = str(cell_value)

                # Escape LaTeX special characters
                cell_value = _escape_latex_chars(cell_value)

                # Handle newlines
                if "\n" in cell_value:
                    cell_value = cell_value.replace("\n", "\\\\\n")

            if col in merge_columns:
                span_info = merge_info[col].get(i)
                if span_info:
                    # This is the first cell of a merged group
                    if span_info["rowspan"] > 1:
                        row_parts.append(f"\\multirow{{{span_info['rowspan']}}}{{*}}{{{cell_value}}}")
                    else:
                        row_parts.append(str(cell_value))
                else:
                    # This cell is part of a merged cell above, use empty cell
                    row_parts.append("")
            else:
                row_parts.append(str(cell_value))

        latex_parts.append(" & ".join(row_parts) + " \\\\")

    latex_parts.append("\\bottomrule")
    latex_parts.append("\\end{tabular}")
    latex_parts.append("\\end{table}")

    return "\n".join(latex_parts)


def _export_csv(df: pd.DataFrame, **kwargs) -> str:
    """Export as CSV format"""
    default_kwargs = {"index": False, "encoding": "utf-8"}
    default_kwargs.update(kwargs)

    output = StringIO()
    df.to_csv(output, **default_kwargs)
    return output.getvalue()


def _calculate_merge_spans(df: pd.DataFrame, merge_columns: list[str]) -> dict:
    """Calculate rowspan information for merged cells"""
    merge_info = {col: {} for col in merge_columns}

    for col in merge_columns:
        if col not in df.columns:
            continue

        current_value = None
        span_start = 0
        span_length = 1

        for i in range(len(df)):
            value = df.iloc[i][col]

            if current_value is None and value is None:
                is_same = True
            elif current_value is None or value is None:
                is_same = False
            else:
                is_same = current_value == value

            if i == 0:
                current_value = value
                span_start = i
                span_length = 1
            elif is_same:
                span_length += 1
            else:
                # End current span
                if span_length > 1:
                    merge_info[col][span_start] = {"rowspan": span_length}
                else:
                    merge_info[col][span_start] = {"rowspan": 1}

                # Start new span
                current_value = value
                span_start = i
                span_length = 1

        # Handle last span
        if span_length > 1:
            merge_info[col][span_start] = {"rowspan": span_length}
        else:
            merge_info[col][span_start] = {"rowspan": 1}

    return merge_info


def _export_html(df: pd.DataFrame, merge_columns: list[str] | None = None, **kwargs) -> str:
    """Export as HTML table format with real merged cells"""
    default_kwargs = {
        "index": False,
        "escape": False,
        "table_id": "dataframe-table",
        "classes": "table table-striped table-bordered",
    }
    default_kwargs.update(kwargs)

    if not merge_columns:
        return df.to_html(**default_kwargs)

    # Build HTML manually with merged cells
    html_parts = []

    # Table opening with attributes
    classes = default_kwargs.get("classes", "")
    table_id = default_kwargs.get("table_id", "")
    html_parts.append(f'<table border="1" class="dataframe {classes}" id="{table_id}">')

    # Header
    html_parts.append("  <thead>")
    html_parts.append('    <tr style="text-align: right;">')
    for col in df.columns:
        html_parts.append(f"      <th>{col}</th>")
    html_parts.append("    </tr>")
    html_parts.append("  </thead>")

    # Body with merged cells
    html_parts.append("  <tbody>")

    # Calculate rowspans for merge columns
    merge_info = _calculate_merge_spans(df, merge_columns)

    for i, row in df.iterrows():
        html_parts.append("    <tr>")

        for col in df.columns:
            cell_value = row[col]
            if cell_value is None:
                cell_value = ""

            # Handle newlines in cell content
            if isinstance(cell_value, str) and "\n" in cell_value:
                cell_value = cell_value.replace("\n", "<br>")

            if col in merge_columns:
                # Check if this cell should be rendered or skipped
                span_info = merge_info[col].get(i)
                if span_info:
                    # This is the first cell of a merged group
                    if span_info["rowspan"] > 1:
                        html_parts.append(f'      <td rowspan="{span_info["rowspan"]}">{cell_value}</td>')
                    else:
                        html_parts.append(f"      <td>{cell_value}</td>")
                # If span_info is None, this cell is part of a merged cell above, so skip it
            else:
                html_parts.append(f"      <td>{cell_value}</td>")

        html_parts.append("    </tr>")

    html_parts.append("  </tbody>")
    html_parts.append("</table>")

    return "\n".join(html_parts)


def _export_formatted_text(df: pd.DataFrame, max_rows: int | None = None, max_cols: int | None = None, **kwargs) -> str:
    default_kwargs = {
        "index": False,
        "max_cols": max_cols,
        "max_rows": max_rows,
        "width": None,
        "max_colwidth": None,
    }
    default_kwargs.update(kwargs)

    # Set pandas display options
    with pd.option_context(
        "display.max_columns",
        default_kwargs["max_cols"],
        "display.max_rows",
        default_kwargs["max_rows"],
        "display.width",
        default_kwargs["width"],
        "display.max_colwidth",
        default_kwargs["max_colwidth"],
    ):
        return str(df)


def _export_png(
    df: pd.DataFrame,
    output_file: str | Path,
    merge_columns: list[str] | None = None,
    **kwargs,
) -> None:
    """Export DataFrame as PNG image using matplotlib with intelligent row width adjustment"""
    try:
        import matplotlib.pyplot as plt
        from matplotlib.table import Table
    except ImportError:
        raise ImportError("matplotlib is required for PNG export. Install with: pip install matplotlib")

    # Default parameters for PNG export
    default_kwargs = {
        "figsize": None,  # Will be calculated automatically
        "dpi": 300,
        "bbox_inches": "tight",
        "facecolor": "white",
        "edgecolor": "none",
        "title": None,
        "title_fontsize": 14,
        "header_color": "#f0f0f0",
        "alternate_row_color": "#f9f9f9",
        "font_size": 10,
        "max_cell_width": 30,  # Maximum characters per cell before wrapping
        "min_figsize": (8, 6),  # Minimum figure size
        "max_figsize": (20, 16),  # Maximum figure size
    }
    default_kwargs.update(kwargs)

    # Handle merged columns by replacing consecutive duplicates with empty strings
    if merge_columns:
        df = _merge_consecutive_cells(df, merge_columns)

    # Apply intelligent text wrapping to all string columns
    df_wrapped = df.copy()
    max_cell_width = default_kwargs["max_cell_width"]

    for col in df_wrapped.select_dtypes(include=["object"]).columns:
        df_wrapped[col] = (
            df_wrapped[col]
            .astype(str)
            .apply(lambda x: _smart_wrap_text(x, max_cell_width) if len(str(x)) > max_cell_width else str(x))
        )

    # Calculate optimal figure size based on data dimensions
    num_rows, num_cols = len(df_wrapped), len(df_wrapped.columns)

    # Calculate maximum content width and height
    max_content_lines = 1
    for col in df_wrapped.select_dtypes(include=["object"]).columns:
        for val in df_wrapped[col]:
            if isinstance(val, str) and "\n" in val:
                max_content_lines = max(max_content_lines, len(val.split("\n")))

    # Calculate figure size based on content
    base_width = max(6, min(20, num_cols * 2.5))
    base_height = max(4, min(16, (num_rows + 1) * 0.5 * max_content_lines))

    # Apply size constraints
    min_width, min_height = default_kwargs["min_figsize"]
    max_width, max_height = default_kwargs["max_figsize"]

    optimal_figsize = (
        max(min_width, min(max_width, base_width)),
        max(min_height, min(max_height, base_height)),
    )

    # Use provided figsize if specified, otherwise use calculated size
    figsize = default_kwargs["figsize"] or optimal_figsize

    # Create figure and axis
    fig, ax = plt.subplots(figsize=figsize)
    ax.axis("off")

    # Add title if specified
    if default_kwargs["title"]:
        fig.suptitle(default_kwargs["title"], fontsize=default_kwargs["title_fontsize"], y=0.95)

    # Create table
    table_data = []

    # Add header
    table_data.append(list(df_wrapped.columns))

    # Add data rows
    for _, row in df_wrapped.iterrows():
        table_data.append([str(val) if not pd.isna(val) else "" for val in row])

    # Create matplotlib table
    table = ax.table(
        cellText=table_data[1:],  # Data rows
        colLabels=table_data[0],  # Headers
        cellLoc="center",
        loc="center",
    )

    # Style the table
    table.auto_set_font_size(False)
    table.set_fontsize(default_kwargs["font_size"])

    # Calculate scale factor based on content
    scale_x = min(2.0, max(0.8, figsize[0] / (num_cols * 2)))
    scale_y = min(3.0, max(1.0, max_content_lines * 1.2))
    table.scale(scale_x, scale_y)

    # Style header row
    num_cols = len(df_wrapped.columns)
    for i in range(num_cols):
        table[(0, i)].set_facecolor(default_kwargs["header_color"])
        table[(0, i)].set_text_props(weight="bold")
        # Enable text wrapping for header cells
        table[(0, i)].set_text_props(wrap=True)

    # Style data rows with alternating colors
    num_rows = len(df_wrapped) + 1  # +1 for header
    for i in range(1, num_rows):
        for j in range(num_cols):
            cell = table[(i, j)]
            if i % 2 == 0:
                cell.set_facecolor(default_kwargs["alternate_row_color"])
            else:
                cell.set_facecolor("white")
            # Enable text wrapping for data cells
            cell.set_text_props(wrap=True)
            # Adjust cell height for multi-line content
            cell_text = table_data[i][j]
            if isinstance(cell_text, str) and "\n" in cell_text:
                cell.set_height(cell.get_height() * len(cell_text.split("\n")))

    # Save the figure
    output_path = Path(output_file)
    output_path.parent.mkdir(parents=True, exist_ok=True)

    plt.savefig(
        output_path,
        dpi=default_kwargs["dpi"],
        bbox_inches=default_kwargs["bbox_inches"],
        facecolor=default_kwargs["facecolor"],
        edgecolor=default_kwargs["edgecolor"],
    )
    plt.close(fig)  # Close figure to free memory

    logger.info(f"PNG table saved to: {output_path}")
