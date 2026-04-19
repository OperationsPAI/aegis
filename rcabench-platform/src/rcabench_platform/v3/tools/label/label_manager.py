import sqlite3
from pathlib import Path
from typing import Any

import polars as pl
import streamlit as st

from rcabench_platform.v2.tools.label.config import DATABASE_PATH


class LabelManager:
    def __init__(self, db_path: str | None = None) -> None:
        self.db_path = Path(db_path) if db_path else DATABASE_PATH
        self._init_database()

    def _init_database(self) -> None:
        self.db_path.parent.mkdir(parents=True, exist_ok=True)

        with sqlite3.connect(self.db_path) as conn:
            cursor = conn.cursor()

            cursor.execute(
                """
                CREATE TABLE IF NOT EXISTS labels (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    name TEXT UNIQUE NOT NULL,
                    description TEXT,
                    color TEXT DEFAULT '#007bff',
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                )
            """
            )

            cursor.execute(
                """
                CREATE TABLE IF NOT EXISTS dataset_labels (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    dataset_path TEXT NOT NULL,
                    label_id INTEGER NOT NULL,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    FOREIGN KEY (label_id) REFERENCES labels (id),
                    UNIQUE(dataset_path, label_id)
                )
            """
            )

            cursor.execute(
                """
                CREATE TABLE IF NOT EXISTS annotations (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    dataset_path TEXT NOT NULL,
                    annotation_type TEXT NOT NULL,  -- 'root_cause', 'anomaly_type', etc.
                    annotation_value TEXT NOT NULL,
                    confidence REAL DEFAULT 1.0,
                    notes TEXT,
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                )
            """
            )

            cursor.execute(
                """
                CREATE TABLE IF NOT EXISTS selection_templates (
                    id INTEGER PRIMARY KEY AUTOINCREMENT,
                    name TEXT UNIQUE NOT NULL,
                    services TEXT NOT NULL,  -- JSON string of selected services
                    metrics TEXT NOT NULL,   -- JSON string of selected metrics
                    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
                )
            """
            )

            conn.commit()

    def add_label(self, name: str, description: str = "", color: str = "#007bff") -> bool:
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()
                cursor.execute(
                    """
                    INSERT INTO labels (name, description, color)
                    VALUES (?, ?, ?)
                """,
                    (name, description, color),
                )
                conn.commit()
                return True
        except sqlite3.IntegrityError:
            st.error(f"Label '{name}' already exists")
            return False
        except Exception as e:
            st.error(f"Failed to add label: {str(e)}")
            return False

    def get_all_labels(self) -> pl.DataFrame:
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()
                cursor.execute(
                    """
                    SELECT id, name, description, color, created_at
                    FROM labels
                    ORDER BY name
                """
                )
                rows = cursor.fetchall()
                columns = ["id", "name", "description", "color", "created_at"]

                if rows:
                    return pl.DataFrame(rows, schema=columns, orient="row")
                else:
                    return pl.DataFrame(schema=columns)
        except Exception as e:
            st.error(f"Failed to get labels: {str(e)}")
            return pl.DataFrame()

    def delete_label(self, label_id: int) -> bool:
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()

                # First delete associated dataset labels
                cursor.execute("DELETE FROM dataset_labels WHERE label_id = ?", (label_id,))

                # Then delete the label itself
                cursor.execute("DELETE FROM labels WHERE id = ?", (label_id,))

                conn.commit()
                return True
        except Exception as e:
            st.error(f"Failed to delete label: {str(e)}")
            return False

    def assign_labels_to_dataset(self, dataset_path: str, label_ids: list[int]) -> bool:
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()

                cursor.execute("DELETE FROM dataset_labels WHERE dataset_path = ?", (dataset_path,))

                for label_id in label_ids:
                    cursor.execute(
                        """
                        INSERT INTO dataset_labels (dataset_path, label_id)
                        VALUES (?, ?)
                    """,
                        (dataset_path, label_id),
                    )

                conn.commit()
                return True
        except Exception as e:
            st.error(f"Failed to assign label: {str(e)}")
            return False

    def get_dataset_labels(self, dataset_path: str) -> pl.DataFrame:
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()
                cursor.execute(
                    """
                    SELECT l.id, l.name, l.description, l.color, dl.created_at
                    FROM labels l
                    JOIN dataset_labels dl ON l.id = dl.label_id
                    WHERE dl.dataset_path = ?
                    ORDER BY l.name
                """,
                    (dataset_path,),
                )
                rows = cursor.fetchall()
                columns = ["id", "name", "description", "color", "created_at"]

                if rows:
                    return pl.DataFrame(rows, schema=columns, orient="row")
                else:
                    return pl.DataFrame(schema=columns)
        except Exception as e:
            st.error(f"Failed to get dataset labels: {str(e)}")
            return pl.DataFrame()

    def search_datasets_by_labels(self, label_ids: list[int]) -> list[str]:
        if not label_ids:
            return []

        try:
            with sqlite3.connect(self.db_path) as conn:
                placeholders = ",".join(["?"] * len(label_ids))
                cursor = conn.cursor()
                cursor.execute(
                    f"""
                    SELECT DISTINCT dataset_path
                    FROM dataset_labels
                    WHERE label_id IN ({placeholders})
                """,
                    label_ids,
                )

                return [row[0] for row in cursor.fetchall()]
        except Exception as e:
            st.error(f"Failed to search datasets: {str(e)}")
            return []

    def add_annotation(
        self,
        dataset_path: str,
        annotation_type: str,
        annotation_value: str,
        confidence: float = 1.0,
        notes: str = "",
    ) -> bool:
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()
                cursor.execute(
                    """
                    INSERT INTO annotations
                    (dataset_path, annotation_type, annotation_value, confidence, notes)
                    VALUES (?, ?, ?, ?, ?)
                """,
                    (
                        dataset_path,
                        annotation_type,
                        annotation_value,
                        confidence,
                        notes,
                    ),
                )
                conn.commit()
                return True
        except Exception as e:
            st.error(f"Failed to add annotation: {str(e)}")
            return False

    def get_annotations(self, dataset_path: str) -> pl.DataFrame:
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()
                cursor.execute(
                    """
                    SELECT id, annotation_type, annotation_value, confidence, notes,
                           created_at, updated_at
                    FROM annotations
                    WHERE dataset_path = ?
                    ORDER BY created_at DESC
                """,
                    (dataset_path,),
                )
                rows = cursor.fetchall()
                columns = [
                    "id",
                    "annotation_type",
                    "annotation_value",
                    "confidence",
                    "notes",
                    "created_at",
                    "updated_at",
                ]

                if rows:
                    return pl.DataFrame(rows, schema=columns, orient="row")
                else:
                    return pl.DataFrame(schema=columns)
        except Exception as e:
            st.error(f"Failed to get annotations: {str(e)}")
            return pl.DataFrame()

    def update_annotation(self, annotation_id: int, annotation_value: str, confidence: float, notes: str) -> bool:
        """Update annotation"""
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()
                cursor.execute(
                    """
                    UPDATE annotations
                    SET annotation_value = ?, confidence = ?, notes = ?,
                        updated_at = CURRENT_TIMESTAMP
                    WHERE id = ?
                """,
                    (annotation_value, confidence, notes, annotation_id),
                )
                conn.commit()
                return True
        except Exception as e:
            st.error(f"Failed to update annotation: {str(e)}")
            return False

    def delete_annotation(self, annotation_id: int) -> bool:
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()
                cursor.execute("DELETE FROM annotations WHERE id = ?", (annotation_id,))
                conn.commit()
                return True
        except Exception as e:
            st.error(f"Failed to delete annotation: {str(e)}")
            return False

    def get_label_statistics(self) -> dict[str, Any]:
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()

                # Total labels
                cursor.execute("SELECT COUNT(*) FROM labels")
                total_labels = cursor.fetchone()[0]

                # Total datasets
                cursor.execute("SELECT COUNT(DISTINCT dataset_path) FROM dataset_labels")
                total_datasets = cursor.fetchone()[0]

                # Label usage statistics
                cursor.execute(
                    """
                    SELECT l.name, COUNT(dl.dataset_path) as usage_count
                    FROM labels l
                    LEFT JOIN dataset_labels dl ON l.id = dl.label_id
                    GROUP BY l.id, l.name
                    ORDER BY usage_count DESC
                """
                )
                label_usage = dict(cursor.fetchall())

                # Annotation statistics
                cursor.execute("SELECT COUNT(*) FROM annotations")
                total_annotations = cursor.fetchone()[0]

                cursor.execute(
                    """
                    SELECT annotation_type, COUNT(*) as count
                    FROM annotations
                    GROUP BY annotation_type
                    ORDER BY count DESC
                """
                )
                annotation_types = dict(cursor.fetchall())

                return {
                    "total_labels": total_labels,
                    "total_datasets": total_datasets,
                    "total_annotations": total_annotations,
                    "label_usage": label_usage,
                    "annotation_types": annotation_types,
                }
        except Exception as e:
            st.error(f"Failed to get statistics: {str(e)}")
            return {}

    def export_labels_and_annotations(self) -> dict[str, pl.DataFrame]:
        try:
            with sqlite3.connect(self.db_path) as conn:
                # Export labels
                cursor = conn.cursor()
                cursor.execute("SELECT * FROM labels")
                labels_rows = cursor.fetchall()
                labels_columns = ["id", "name", "description", "color", "created_at"]
                labels_df = (
                    pl.DataFrame(labels_rows, schema=labels_columns, orient="row")
                    if labels_rows
                    else pl.DataFrame(schema=labels_columns)
                )

                # Export dataset labels with label names
                cursor.execute(
                    """
                    SELECT dl.*, l.name as label_name
                    FROM dataset_labels dl
                    JOIN labels l ON dl.label_id = l.id
                """
                )
                dataset_labels_rows = cursor.fetchall()
                dataset_labels_columns = [
                    "id",
                    "dataset_path",
                    "label_id",
                    "created_at",
                    "label_name",
                ]
                dataset_labels_df = (
                    pl.DataFrame(dataset_labels_rows, schema=dataset_labels_columns, orient="row")
                    if dataset_labels_rows
                    else pl.DataFrame(schema=dataset_labels_columns)
                )

                # Export annotations
                cursor.execute("SELECT * FROM annotations")
                annotations_rows = cursor.fetchall()
                annotations_columns = [
                    "id",
                    "dataset_path",
                    "annotation_type",
                    "annotation_value",
                    "confidence",
                    "notes",
                    "created_at",
                    "updated_at",
                ]
                annotations_df = (
                    pl.DataFrame(annotations_rows, schema=annotations_columns, orient="row")
                    if annotations_rows
                    else pl.DataFrame(schema=annotations_columns)
                )

                return {
                    "labels": labels_df,
                    "dataset_labels": dataset_labels_df,
                    "annotations": annotations_df,
                }
        except Exception as e:
            st.error(f"Failed to export data: {str(e)}")
            return {}

    def import_labels_from_csv(self, csv_content: str) -> bool:
        """Import labels from CSV"""
        try:
            import io

            df = pl.read_csv(io.StringIO(csv_content))

            required_columns = ["name"]
            if not all(col in df.columns for col in required_columns):
                st.error("CSV file must contain 'name' column")
                return False

            success_count = 0
            for row in df.iter_rows(named=True):
                name = row["name"]
                description = row.get("description", "")
                color = row.get("color", "#007bff")

                if self.add_label(name, description, color):
                    success_count += 1

            st.success(f"Successfully imported {success_count} labels")
            return True
        except Exception as e:
            st.error(f"Failed to import labels: {str(e)}")
            return False

    def save_selection_template(self, name: str, services: list[str], metrics: list[str]) -> bool:
        try:
            import json

            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()
                cursor.execute(
                    """
                    INSERT INTO selection_templates (name, services, metrics)
                    VALUES (?, ?, ?)
                """,
                    (name, json.dumps(services), json.dumps(metrics)),
                )
                conn.commit()
                return True
        except sqlite3.IntegrityError:
            st.error(f"Template '{name}' already exists")
            return False
        except Exception as e:
            st.error(f"Failed to save template: {str(e)}")
            return False

    def get_all_selection_templates(self) -> pl.DataFrame:
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()
                cursor.execute(
                    """
                    SELECT id, name, services, metrics, created_at
                    FROM selection_templates
                    ORDER BY name
                """
                )
                rows = cursor.fetchall()
                columns = ["id", "name", "services", "metrics", "created_at"]

                if rows:
                    return pl.DataFrame(rows, schema=columns, orient="row")
                else:
                    return pl.DataFrame(schema=columns)
        except Exception as e:
            st.error(f"Failed to get templates: {str(e)}")
            return pl.DataFrame()

    def load_selection_template(self, template_id: int) -> dict[str, Any] | None:
        try:
            import json

            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()
                cursor.execute(
                    "SELECT services, metrics FROM selection_templates WHERE id = ?",
                    (template_id,),
                )
                row = cursor.fetchone()

                if row:
                    return {
                        "services": json.loads(row[0]),
                        "metrics": json.loads(row[1]),
                    }
                return None
        except Exception as e:
            st.error(f"Failed to load template: {str(e)}")
            return None

    def delete_selection_template(self, template_id: int) -> bool:
        try:
            with sqlite3.connect(self.db_path) as conn:
                cursor = conn.cursor()
                cursor.execute("DELETE FROM selection_templates WHERE id = ?", (template_id,))
                conn.commit()
                return True
        except Exception as e:
            st.error(f"Failed to delete template: {str(e)}")
            return False


@st.cache_resource
def get_label_manager() -> LabelManager:
    return LabelManager()
